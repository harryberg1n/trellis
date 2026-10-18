//go:build !windows

package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestModes_StateIsOwnerOnly(t *testing.T) {
	s, dir, _ := open(t)
	if err := s.Park(ev("edit", "tree/p")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ev("edit", "tree/a")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), append(mustRead(t, filepath.Join(dir, "events.jsonl")), []byte("junk\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(1); err != nil {
		t.Fatal(err)
	}
	if err := s.Allow("main-commit"); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		want := fs.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", p, got, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
