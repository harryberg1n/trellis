package run

import (
	"bufio"
	"errors"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
)

func TestLight_TreeKillEndsGrandchildren(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv(roleEnv, "child")
	t.Setenv(addrEnv, ln.Addr().String())

	fc := clock.Fake(time.Unix(1000, 0))
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := Light(fc, Spec{Name: os.Args[0], Timeout: 30 * time.Second})
		done <- outcome{res, err}
	}()

	// The child tells us the grandchild's pid once the tree exists.
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	conn.Close()
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

	fc.BlockUntil(1)
	fc.Advance(30 * time.Second)
	got := <-done
	if !errors.Is(got.err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", got.err)
	}
	// Death can trail the kill by a moment; yield rather than sleep.
	for alive(pid) {
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
	res, err := Light(fc, Spec{Name: "git", Args: []string{"no-such-subcommand"}, Timeout: time.Minute})
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
