package engine

import (
	"os"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/store"
)

func TestStep_DurableByDefaultLazyOnRequest(t *testing.T) {
	s, _, _ := open(t)
	n := 0
	s.SetFsync(func(f *os.File) error { n++; return f.Sync() })

	if _, err := Step(s, nil, ev(kernel.EvEdit, "l"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("default Step synced %d times, want 2 (log, checkpoint)", n)
	}

	n = 0
	if _, err := Step(s, nil, ev(kernel.EvEdit, "l"), kernel.Context{}, WithDurability(store.Lazy)); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("lazy Step synced %d times, want 0", n)
	}
	evs, err := s.Read(1)
	if err != nil || len(evs) != 2 {
		t.Fatalf("read %d events, %v; want 2", len(evs), err)
	}
}
