package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// syncCount makes the store count its fsyncs, recording the base name of each
// file synced.
func syncCount(s *Store) *[]string {
	var names []string
	s.SetFsync(func(f *os.File) error {
		names = append(names, filepath.Base(f.Name()))
		return f.Sync()
	})
	return &names
}

func TestStore_LazyAppendSkipsFsync(t *testing.T) {
	s, _, _ := open(t)
	synced := syncCount(s)

	err := s.Locked(func(tx *Tx) error {
		tx.SetDurability(Lazy)
		if _, err := tx.Append(kernel.Event{Kind: "edit", Lane: "l", Session: "s"}); err != nil {
			return err
		}
		return tx.Save(Checkpoint{LastSeq: 1})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(*synced) != 0 {
		t.Errorf("a lazy append and save synced %v, want nothing", *synced)
	}

	err = s.Locked(func(tx *Tx) error {
		if _, err := tx.Append(kernel.Event{Kind: "edit", Lane: "l", Session: "s"}); err != nil {
			return err
		}
		return tx.Save(Checkpoint{LastSeq: 2})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(*synced) != 2 {
		t.Errorf("a durable append and save synced %v, want 2 (log, checkpoint)", *synced)
	}
	evs, err := s.Read(1)
	if err != nil || len(evs) != 2 {
		t.Fatalf("read %d events, %v; want both", len(evs), err)
	}
}

func TestStore_SyncFsyncsLogAndCheckpoint(t *testing.T) {
	s, _, _ := open(t)
	err := s.Locked(func(tx *Tx) error {
		tx.SetDurability(Lazy)
		if _, err := tx.Append(kernel.Event{Kind: "edit", Lane: "l", Session: "s"}); err != nil {
			return err
		}
		return tx.Save(Checkpoint{LastSeq: 1})
	})
	if err != nil {
		t.Fatal(err)
	}
	synced := syncCount(s)

	if err := s.Sync(); err != nil {
		t.Fatal(err)
	}
	want := []string{"events.jsonl", "state.json"}
	if len(*synced) != 2 || (*synced)[0] != want[0] || (*synced)[1] != want[1] {
		t.Errorf("Sync synced %v, want %v", *synced, want)
	}
}

func TestStore_SyncOnAnEmptyStoreIsFine(t *testing.T) {
	s, _, _ := open(t)
	if err := s.Sync(); err != nil {
		t.Errorf("Sync on a store with no log or checkpoint: %v", err)
	}
}
