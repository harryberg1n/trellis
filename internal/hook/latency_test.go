//go:build !race

package hook

// The latency tests are left out of the -race build: the race detector slows
// every call several times over, so a p95 taken under it says nothing about
// the hook a user waits on. CI runs them in a step of their own, without -race:
// go test ./internal/hook -run Latency -count=1

import (
	"bytes"
	"sort"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/testsupport"
)

const (
	editP95Limit = 20 * time.Millisecond
	stopP95Limit = 300 * time.Millisecond
	samples      = 1000
	stopSamples  = 200
)

// p95 is the 950th of 1000 sorted durations.
func p95(d []time.Duration) time.Duration {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)*95/100-1]
}

// timeEdits runs n generated Edit/Write payloads through Run, timing each.
func timeEdits(t testing.TB, n int) []time.Duration {
	e := newEnv(t)
	gens := generate(t, e.repo, n)
	out := make([]time.Duration, n)
	var stdout, stderr bytes.Buffer
	for i, g := range gens {
		r := bytes.NewReader(g.payload)
		start := time.Now()
		code := Run("PostToolUse", r, &stdout, &stderr, e.deps)
		out[i] = time.Since(start)
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("payload %d: exit %d, stdout %q, stderr %q", i, code, stdout.String(), stderr.String())
		}
	}
	if got := len(e.events(t)); got != n {
		t.Fatalf("%d events for %d payloads", got, n)
	}
	return out
}

func TestHookLatency_EditP95Under20ms(t *testing.T) {
	if testing.Short() {
		t.Skip("latency test: skipped by -short")
	}
	got := p95(timeEdits(t, samples))
	t.Logf("edit p95 = %v", got)
	if got > editP95Limit {
		t.Errorf("edit p95 = %v, want <= %v", got, editP95Limit)
	}
}

func TestHookLatency_StopP95Under300ms(t *testing.T) {
	if testing.Short() {
		t.Skip("latency test: skipped by -short")
	}
	e := newEnv(t)
	stop := fill(readShape(t, "stop"), vars(e.repo, "", ""))
	gens := generate(t, e.repo, 8)
	times := make([]time.Duration, stopSamples)
	var stdout, stderr bytes.Buffer
	for i := range times {
		// A turn: one Edit of one of eight files, then Stop. Only Stop is
		// timed. The files stay few, as in a real turn, so the anchor's cost
		// does not grow with the run.
		g := gens[i%len(gens)]
		testsupport.Write(t, g.file, text(i, 200+i%500))
		if code := Run("PostToolUse", bytes.NewReader(g.payload), &stdout, &stderr, e.deps); code != 0 {
			t.Fatalf("edit %d: exit %d", i, code)
		}
		start := time.Now()
		code := Run("Stop", bytes.NewReader(stop), &stdout, &stderr, e.deps)
		times[i] = time.Since(start)
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("stop %d: exit %d, stdout %q, stderr %q", i, code, stdout.String(), stderr.String())
		}
	}
	for _, ev := range e.events(t) {
		if ev.Kind == "internal.error" {
			t.Fatalf("a hook failed: %v", ev.Detail)
		}
	}
	got := p95(times)
	t.Logf("stop p95 = %v over %d stops", got, stopSamples)
	if got > stopP95Limit {
		t.Errorf("stop p95 = %v, want <= %v", got, stopP95Limit)
	}
}

func BenchmarkHook_EditP95(b *testing.B) {
	e := newEnv(b)
	gens := generate(b, e.repo, samples)
	var times []time.Duration
	var stdout, stderr bytes.Buffer
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		g := gens[i%len(gens)]
		start := time.Now()
		Run("PostToolUse", bytes.NewReader(g.payload), &stdout, &stderr, e.deps)
		times = append(times, time.Since(start))
	}
	b.ReportMetric(float64(p95(times))/float64(time.Millisecond), "p95-ms")
}
