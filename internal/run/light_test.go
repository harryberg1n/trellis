package run

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestLight_TreeKillEndsGrandchildren(t *testing.T) { treeKill(t, false) }

// A cancelled context ends the child's tree at once, without the timeout.
func TestLight_CancelledContextKillsTheChildTree(t *testing.T) { treeKill(t, true) }

// treeKill starts a child with a grandchild and ends the run by the timeout, or
// with cancelled the cancelled context, then requires the grandchild dead.
func treeKill(t *testing.T, cancelled bool) {
	t.Helper()
	// Real time bounds the test so a broken kill fails instead of hanging; the
	// kill itself is driven by the fake clock.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if err := ln.(*net.TCPListener).SetDeadline(time.Now().Add(60 * time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Setenv(roleEnv, "child")
	t.Setenv(addrEnv, ln.Addr().String())

	fc := clock.Fake(time.Unix(1000, 0))
	// However the test ends, fire the timeout so no child tree outlives it.
	t.Cleanup(func() { fc.Advance(time.Hour) })
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)
	go func() {
		res, err := Light(fc, Spec{Name: os.Args[0], Timeout: 30 * time.Second, Ctx: runCtx})
		done <- outcome{res, err}
	}()

	// The child tells us the grandchild's pid once the tree exists.
	conn, err := ln.Accept()
	if err != nil {
		t.Fatalf("the child never reported its grandchild: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.Close() // read-only use; the read error below is the one that matters
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	if !alive(pid) {
		t.Fatalf("grandchild %d is not running before the timeout", pid)
	}

	timerSet := make(chan struct{})
	go func() { fc.BlockUntil(1); close(timerSet) }()
	select {
	case <-timerSet:
	case <-ctx.Done():
		t.Fatal("Light never waited on the clock")
	}
	want := ErrTimeout
	if cancelled {
		want = context.Canceled
		cancelRun()
	} else {
		fc.Advance(30 * time.Second)
	}
	select {
	case got := <-done:
		if !errors.Is(got.err, want) || (cancelled && errors.Is(got.err, ErrTimeout)) {
			t.Fatalf("err = %v, want %v", got.err, want)
		}
	case <-ctx.Done():
		t.Fatal("Light did not return after its timeout")
	}
	// Death can trail the kill by a moment; yield a fixed number of times
	// rather than sleep.
	for i := 0; alive(pid); i++ {
		if i == 5_000_000 {
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill() // best effort, so it is not left behind; the Fatalf below reports
			}
			t.Fatalf("grandchild %d is still alive after the tree kill", pid)
		}
		runtime.Gosched()
	}
}

func TestLight_RunsAndCapturesOutput(t *testing.T) {
	t.Setenv(roleEnv, "")
	fc := clock.Fake(time.Unix(1000, 0))
	res, err := Light(fc, Spec{Name: "git", Args: []string{"--version"}, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(res.Stdout), "git version ") || res.ExitCode != 0 {
		t.Fatalf("stdout %q exit %d", res.Stdout, res.ExitCode)
	}
}

func TestLight_NonZeroExitIsAnError(t *testing.T) {
	fc := clock.Fake(time.Unix(1000, 0))
	res, err := Light(fc, Spec{Name: "git", Args: []string{"rev-parse", "--verify", "no-such-ref"}, Timeout: time.Minute})
	if err == nil || res.ExitCode == 0 {
		t.Fatalf("err %v exit %d, want an error and a non-zero exit", err, res.ExitCode)
	}
}

func TestLight_RefusesNoTimeout(t *testing.T) {
	fc := clock.Fake(time.Unix(1000, 0))
	if _, err := Light(fc, Spec{Name: "git", Args: []string{"--version"}}); !errors.Is(err, ErrNoTimeout) {
		t.Fatalf("err = %v, want ErrNoTimeout", err)
	}
}

func TestLight_PassesStdin(t *testing.T) {
	fc := clock.Fake(time.Unix(1000, 0))
	res, err := Light(fc, Spec{Name: "git", Args: []string{"hash-object", "--stdin"}, Stdin: []byte("hello\n"), Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(res.Stdout)); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Fatalf("id %q", got)
	}
}

func TestLight_ScrubsGitEnv(t *testing.T) {
	repo := testiso.Repo(t)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "no-such-dir"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "no-such-index"))
	t.Setenv("GIT_WORK_TREE", filepath.Join(t.TempDir(), "no-such-tree"))
	fc := clock.Fake(time.Unix(1000, 0))
	res, err := Light(fc, Spec{Name: "git", Args: []string{"rev-parse", "--git-dir"}, Dir: repo, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(res.Stdout)); got != ".git" {
		t.Fatalf("git-dir = %q, want .git", got)
	}
}

func TestScrubGitEnv(t *testing.T) {
	in := []string{
		"PATH=/bin", "GIT_DIR=x", "GIT_WORK_TREE=y", "GIT_INDEX_FILE=z", "GIT_OBJECT_DIRECTORY=o",
		"GIT_CONFIG_GLOBAL=g", "GIT_CONFIG_NOSYSTEM=1", "GIT_CEILING_DIRECTORIES=c", "GIT_TERMINAL_PROMPT=0", "HOME=/h",
	}
	want := []string{"PATH=/bin", "GIT_CONFIG_GLOBAL=g", "GIT_CONFIG_NOSYSTEM=1", "GIT_CEILING_DIRECTORIES=c", "GIT_TERMINAL_PROMPT=0", "HOME=/h"}
	if got := scrubGitEnv(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLight_ACancelledContextNeverStartsTheChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fc := clock.Fake(time.Unix(1000, 0))

	_, err := Light(fc, Spec{Name: "git", Args: []string{"--version"}, Timeout: time.Minute, Ctx: ctx})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWithContext_GivesTheContextToAChildThatHasNone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var got []context.Context
	inner := &recordCtx{func(s Spec) { got = append(got, s.Ctx) }}
	r := WithContext(inner, ctx)
	own := context.WithValue(context.Background(), struct{}{}, 1)

	_, _ = r.Light(Spec{})
	_, _ = r.Light(Spec{Ctx: own})
	cancel()

	if len(got) != 2 || got[0] != ctx || got[1] != own {
		t.Errorf("contexts = %v, want the wrapper's and then the spec's own", got)
	}
	if WithContext(inner, nil) != Runner(inner) {
		t.Errorf("a nil context wrapped the runner")
	}
}

type recordCtx struct{ f func(Spec) }

func (r *recordCtx) Light(s Spec) (Result, error) { r.f(s); return Result{}, nil }
