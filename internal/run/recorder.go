package run

import "sync"

// Recorder wraps a Runner and records every Spec it is asked to run. It is the
// spawn counter seam: a test asserts Count() or the set of Kinds().
type Recorder struct {
	Inner Runner

	mu    sync.Mutex
	specs []Spec
}

// Light records spec, then runs it on Inner.
func (r *Recorder) Light(spec Spec) (Result, error) {
	r.mu.Lock()
	r.specs = append(r.specs, spec)
	r.mu.Unlock()
	return r.Inner.Light(spec)
}

// Specs is every Spec run so far, in order.
func (r *Recorder) Specs() []Spec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Spec(nil), r.specs...)
}

// Count is the number of children spawned.
func (r *Recorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.specs)
}

// Kinds is each spawn's kind in order.
func (r *Recorder) Kinds() []string {
	var out []string
	for _, s := range r.Specs() {
		out = append(out, Kind(s))
	}
	return out
}

// Kind is the git subcommand of a git spec (leading "-c key=value" pairs
// skipped), else the program name.
func Kind(s Spec) string {
	if s.Name != "git" {
		return s.Name
	}
	args := s.Args
	for len(args) >= 2 && args[0] == "-c" {
		args = args[2:]
	}
	if len(args) == 0 {
		return s.Name
	}
	return args[0]
}
