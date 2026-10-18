package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomic_PublishesTheDataAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAtomic(dir, "f.json", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(dir, "f.json", []byte("two")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "f.json"))
	if err != nil || string(got) != "two" {
		t.Fatalf("f.json = %q, %v; want two", got, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d entries in the directory, want only f.json", len(entries))
	}
}

func TestWriteAtomic_SyncsOnlyWhenAsked(t *testing.T) {
	dir := t.TempDir()
	synced := 0
	count := func(*os.File) error { synced++; return nil }
	if err := WriteAtomic(dir, "a", []byte("x")); err != nil || synced != 0 {
		t.Fatalf("plain write: err %v, %d syncs; want none", err, synced)
	}
	if err := WriteAtomic(dir, "a", []byte("x"), WithSync(count)); err != nil || synced != 1 {
		t.Fatalf("synced write: err %v, %d syncs; want 1", err, synced)
	}
	failing := func(*os.File) error { return errors.New("disk gone") }
	if err := WriteAtomic(dir, "a", []byte("y"), WithSync(failing)); err == nil {
		t.Fatal("a failing sync was swallowed")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a")); string(got) != "x" {
		t.Errorf("a = %q after a failed write, want the old x", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d entries, want the temp removed", len(entries))
	}
}

func TestWriteAtomic_RenameFailureKeepsTheOldFileAndRemovesTheTemp(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAtomic(dir, "s", []byte("old")); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("rename refused")
	err := WriteAtomic(dir, "s", []byte("new"), WithRename(func(string, string) error { return boom }))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the rename error", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "s")); string(got) != "old" {
		t.Errorf("s = %q, want old", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d entries, want the temp removed", len(entries))
	}
}

func TestWriteAtomic_ModeAndTempPattern(t *testing.T) {
	dir := t.TempDir()
	var tmpName string
	rename := func(from, to string) error { tmpName = filepath.Base(from); return os.Rename(from, to) }
	if err := WriteAtomic(dir, "bin", []byte("x"), WithMode(0o444), WithTempPattern(".pin-*"), WithRename(rename)); err != nil {
		t.Fatal(err)
	}
	if matched, _ := filepath.Match(".pin-*", tmpName); !matched {
		t.Errorf("temp %q does not follow the pattern .pin-*", tmpName)
	}
	info, err := os.Stat(filepath.Join(dir, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o200 != 0 {
		t.Errorf("mode %v, want read-only", info.Mode().Perm())
	}
}
