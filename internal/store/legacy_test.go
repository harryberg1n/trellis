package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
)

func TestRead_AcceptsV001LaneRecords(t *testing.T) {
	s, dir, _ := open(t)
	raw, err := os.ReadFile(filepath.Join("testdata", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		seq  uint64
		kind string
		tree string
	}{
		{1, "tree.opened", "lane/a"},
		{2, "edit", "lane/a"},
		{3, "tree.merged", "lane/a"},
		{4, "commit", "tree/b"}, // tree wins over lane when both are present
	}
	if len(got) != len(want) {
		t.Fatalf("read %d events, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Seq != w.seq || got[i].Kind != w.kind || got[i].Tree != w.tree {
			t.Errorf("event %d = seq %d kind %q tree %q, want seq %d kind %q tree %q",
				i, got[i].Seq, got[i].Kind, got[i].Tree, w.seq, w.kind, w.tree)
		}
	}
	if got[0].Arms["exp1"] != "control" || got[2].Detail["pr"] != "7" {
		t.Errorf("arms/detail lost: %+v %+v", got[0], got[2])
	}
}

func TestRead_RewrittenLogUsesOnlyTheNewSpelling(t *testing.T) {
	s, dir, _ := open(t)
	raw, err := os.ReadFile(filepath.Join("testdata", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := s.Read(0)
	if err != nil {
		t.Fatal(err)
	}
	fresh, dir2, _ := open(t)
	if _, err := fresh.Append(old...); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir2, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`"lane"`, `lane.opened`, `lane.merged`} {
		if bytes.Contains(out, []byte(bad)) {
			t.Errorf("re-written log holds %s:\n%s", bad, out)
		}
	}
	if !bytes.Contains(out, []byte(`"kind":"tree.opened","tree":"lane/a"`)) {
		t.Errorf("re-written log lacks the tree record:\n%s", out)
	}
}

func TestCheckpoint_AcceptsLanesKey(t *testing.T) {
	s, dir, _ := open(t)
	old := `{"last_seq":9,"arms":{"lane/a":{"exp1":"control"}},"lanes":{"lane/a":2}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	var got Checkpoint
	err := s.Locked(func(tx *Tx) error {
		var err error
		got, err = tx.Checkpoint()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSeq != 9 || got.Trees["lane/a"] != kernel.TreeMerged || got.Arms["lane/a"]["exp1"] != "control" {
		t.Fatalf("checkpoint = %+v, want last_seq 9, lane/a merged", got)
	}
	err = s.Locked(func(tx *Tx) error { return tx.Save(got) })
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(saved, []byte(`"lanes"`)) || !bytes.Contains(saved, []byte(`"trees"`)) {
		t.Errorf("saved state.json = %s, want only the trees key", saved)
	}
}
