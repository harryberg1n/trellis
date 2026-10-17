package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
)

func TestCheckpoint_RoundTrip(t *testing.T) {
	s, _, _ := open(t)
	err := s.Locked(func(tx *Tx) error {
		return tx.Save(Checkpoint{
			LastSeq: 7,
			Arms:    map[string]map[string]string{"lane/a": {"exp1": "control"}},
			Lanes:   map[string]kernel.LaneState{"lane/a": kernel.LaneOpen},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	var got Checkpoint
	err = s.Locked(func(tx *Tx) error {
		var err error
		got, err = tx.Checkpoint()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSeq != 7 || got.Arms["lane/a"]["exp1"] != "control" || got.Lanes["lane/a"] != kernel.LaneOpen {
		t.Fatalf("checkpoint = %+v", got)
	}
}

func TestCheckpoint_MissingFileIsEmpty(t *testing.T) {
	s, _, _ := open(t)
	err := s.Locked(func(tx *Tx) error {
		cp, err := tx.Checkpoint()
		if err != nil || cp.LastSeq != 0 || len(cp.Arms) != 0 {
			t.Fatalf("checkpoint = %+v, %v", cp, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCheckpoint_TornWriteKeepsTheOldFile(t *testing.T) {
	s, dir, _ := open(t)
	save := func(seq uint64) error {
		return s.Locked(func(tx *Tx) error { return tx.Save(Checkpoint{LastSeq: seq}) })
	}
	if err := save(1); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s.rename = func(string, string) error { return errors.New("injected rename failure") }
	if err := save(2); err == nil {
		t.Fatal("save succeeded despite the rename failure")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("state.json changed: %q -> %q (%v)", before, after, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "state.json.*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}
