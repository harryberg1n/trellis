package tree

// Copied from aphrollo-tools internal/workspace/nettransient.go at 12b3724e
// (maxNetFailures, netRetryBase, netRetry.failed). Incident: gh-500-retry, a
// wait met one 500 in a read of the check runs and gave up on a pull request
// that was fine. What changed: the wait is the env's clock, and the retry
// wraps one read instead of living in a poll state.

import (
	"io"
	"strings"
	"sync"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/host"
)

const (
	// maxGHTries is how many times a gh read is made before its failure stands.
	maxGHTries = 5
	// ghRetryBase is the wait after the first failure; it doubles after each.
	ghRetryBase = 5 * time.Second
)

// retry makes read until it succeeds, fails for a reason that is not transient
// (see host.Transient), or has failed maxGHTries times; the last error stands.
// Between tries it waits ghRetryBase, doubled each time, or the Retry-After a
// rate limit named (at most two minutes) when that is longer, and says so.
func (e Env) retry(what string, read func() error) error {
	for try := 1; ; try++ {
		err := read()
		if err == nil || try == maxGHTries || !host.Transient(err) || e.cancelled() {
			return err
		}
		wait := max(ghRetryBase<<(try-1), host.RetryAfter(err))
		e.sayWait("%s: GitHub could not be reached (%d of %d tries): %s; trying again in %s", what, try, maxGHTries, firstLine(err), wait)
		e.wait(wait)
	}
}

// sayWait is say for a line about waiting: it starts with the UTC time of the
// env's clock, as "15:04:05 ", so the gaps of a long merge can be read off the
// log.
func (e Env) sayWait(format string, a ...any) {
	clk := e.Clock
	if clk == nil {
		clk = clock.Real()
	}
	e.say("%s "+format, append([]any{clk.Now().UTC().Format("15:04:05")}, a...)...)
}

// lastLine keeps the last non-empty line of what is written through it, and
// passes every byte on to out as it comes. It is the writer of a streamed
// --check: the operator sees the output live, the failure summary names its
// last line.
type lastLine struct {
	mu   sync.Mutex
	out  io.Writer
	last string // the last complete non-empty line
	cur  []byte // the line being written
}

// maxLineKept bounds the part of one line lastLine remembers.
const maxLineKept = 4096

func (w *lastLine) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// A verb output that fails must not stop the child: its pipe is read to the
	// end, and the last line is kept all the same.
	if w.out != nil {
		_, _ = w.out.Write(p)
	}
	for _, b := range p {
		if b == '\n' || b == '\r' {
			w.endLine()
			continue
		}
		if len(w.cur) < maxLineKept {
			w.cur = append(w.cur, b)
		}
	}
	return len(p), nil
}

func (w *lastLine) endLine() {
	if line := strings.TrimSpace(string(w.cur)); line != "" {
		w.last = line
	}
	w.cur = w.cur[:0]
}

// Last is the last non-empty line written so far, a line still being written
// included; "" when there is none.
func (w *lastLine) Last() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if line := strings.TrimSpace(string(w.cur)); line != "" {
		return line
	}
	return w.last
}

var _ io.Writer = (*lastLine)(nil)
