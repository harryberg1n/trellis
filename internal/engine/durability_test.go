package engine

import (
	"os"
	"path/filepath"
	"strings"
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

func TestCheckpoint_CorruptFileHealsByReplay(t *testing.T) {
	for name, junk := range map[string]string{"junk": "{\"last_seq\": 1, \"arm", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			s, dir, _ := open(t)
			if _, err := Step(s, nil, ev(kernel.EvEdit, "lane/a"), kernel.Context{}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(junk), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := Step(s, nil, ev(kernel.EvEdit, "lane/a"), kernel.Context{}); err != nil {
				t.Fatalf("Step over a corrupt state.json: %v", err)
			}

			var cp store.Checkpoint
			err := s.Locked(func(tx *store.Tx) error {
				var err error
				cp, err = tx.Checkpoint()
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if cp.LastSeq != 2 || cp.Lanes["lane/a"] != kernel.LaneOpen {
				t.Errorf("checkpoint after healing = %+v, want last_seq 2 and lane/a open", cp)
			}
			if junk != "" {
				q, err := os.ReadFile(filepath.Join(dir, "events.quarantine"))
				if err != nil || !strings.Contains(string(q), junk) {
					t.Errorf("quarantine = %q, %v; want the corrupt state.json kept", q, err)
				}
			}
		})
	}
}
