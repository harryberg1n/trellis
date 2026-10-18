// Package run starts child processes for trellis. Light is the only runner in
// M0: a child with a timeout, the plain environment and a process-tree kill.
// Memory and CPU caps come in M1.
package run

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
)

// ErrTimeout is what Light reports for a child the timeout ended.
var ErrTimeout = errors.New("run: the child timed out and its process tree was ended")

// ErrNoTimeout refuses a light child with no timeout: it could hang its caller.
var ErrNoTimeout = errors.New("run: a light child needs a timeout")

// Spec is one child to run. Name is the program, looked up on PATH.
type Spec struct {
	Name    string
	Args    []string
	Dir     string
	Timeout time.Duration
	Stdin   []byte
	// Direct marks a child the user's own verb asked for (trellis tree ...):
	// it may write git state, which the refusal of ErrOwnGitState keeps from
	// the children of tools.
	Direct bool
}

// Result is what a finished child left.
type Result struct {
	Stdout, Stderr []byte
	ExitCode       int
}

// Runner runs light children. Production code uses Exec; tests wrap it in a
// Recorder to count and list what was spawned.
type Runner interface {
	Light(spec Spec) (Result, error)
}

// Exec is the real Runner. Its timeouts are driven by Clock.
type Exec struct{ Clock clock.Clock }

// Light runs spec with Light.
func (e Exec) Light(spec Spec) (Result, error) { return Light(e.Clock, spec) }

// noPrompt keeps a light git child from asking a terminal a question: on unix
// it leads a process group of its own, and a read from /dev/tty would stop it
// until the timeout.
var noPrompt = []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"}

// tree guards a child and everything it starts.
type tree interface {
	attach(p *os.Process) error // after Start, before the child is waited on
	kill()                      // end the child and its descendants
	finish()                    // the child is done; release the guard
}

// Light runs a child to completion in the plain environment. When the timeout,
// read from c, fires first, the child's whole process tree is killed and the
// error wraps ErrTimeout. A non-zero exit is an error, with the Result filled.
// A git child that would write a ref outside refs/trellis/ or a tree's index
// is refused with ErrOwnGitState before it starts.
func Light(c clock.Clock, spec Spec) (Result, error) {
	if !spec.Direct {
		if err := ownGitState(spec); err != nil {
			return Result{}, err
		}
	}
	if spec.Timeout <= 0 {
		return Result{}, ErrNoTimeout
	}
	cmd := exec.Command(spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	env := os.Environ()
	if isGit(spec.Name) {
		env = scrubGitEnv(env)
	}
	env = append(env, noPrompt...)
	cmd.Env = env
	if spec.Stdin != nil {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	tr, err := prepare(cmd)
	if err != nil {
		return Result{}, err
	}
	if err := cmd.Start(); err != nil {
		tr.finish()
		return Result{}, err
	}
	if err := tr.attach(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		tr.finish()
		return Result{}, fmt.Errorf("run: guarding %s: %w", spec.Name, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := c.After(spec.Timeout)
	var waitErr error
	timedOut := false
	select {
	case waitErr = <-done:
	case <-timer:
		timedOut = true
		tr.kill()
		waitErr = <-done
	}
	tr.finish()
	res := Result{Stdout: out.Bytes(), Stderr: errb.Bytes(), ExitCode: cmd.ProcessState.ExitCode()}
	if timedOut {
		return res, errors.Join(ErrTimeout, waitErr)
	}
	if waitErr != nil {
		return res, fmt.Errorf("%s: %w: %s", spec.Name, waitErr, bytes.TrimSpace(errb.Bytes()))
	}
	return res, nil
}

func isGit(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	return base == "git" || base == "git.exe"
}

// gitKeep are the GIT_* variables a git child keeps: configuration and safety
// (the sandbox and the user's own), tracing and prompting. Every other GIT_*
// variable, GIT_DIR, GIT_WORK_TREE and GIT_INDEX_FILE above all, would point
// the child at a repository other than its Dir.
var gitKeep = []string{
	"GIT_CONFIG", "GIT_CEILING_DIRECTORIES", "GIT_EXEC_PATH", "GIT_TERMINAL_PROMPT",
	"GIT_ASKPASS", "GIT_SSH", "GIT_TRACE", "GIT_TEMPLATE_DIR", "GIT_ATTR_NOSYSTEM",
}

// scrubGitEnv is env without the GIT_* variables a git child must not inherit.
func scrubGitEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_") && !keepsGit(kv) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func keepsGit(kv string) bool {
	k, _, _ := strings.Cut(strings.ToUpper(kv), "=")
	for _, p := range gitKeep {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}
