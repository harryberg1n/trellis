package store

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileShared_AMissingFileIsNotExist(t *testing.T) {
	_, err := readFileShared(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want one errors.Is reports as fs.ErrNotExist", err)
	}
}

func TestReadFileShared_ReadsTheWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	want := make([]byte, 3<<20+7)
	for i := range want {
		want[i] = byte(i % 251)
	}
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readFileShared(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("read %d bytes, err %v, want all %d", len(got), err, len(want))
	}
}

// A reader that holds the file open must not block the replace: on Windows a
// plain open does, and a poller that keeps it open leaves the writer blocked.
func TestWriteAtomic_ReplacesAFileASharedReaderHoldsOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := WriteAtomic(dir, "state.json", []byte("old")); err != nil {
		t.Fatal(err)
	}
	held, err := openShared(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	// A stepping clock: a replace the reader blocked would be retried to the
	// bound and fail, with no real time spent either way.
	c := &stepClock{}
	if err := WriteAtomic(dir, "state.json", []byte("new"), WithClock(c)); err != nil {
		t.Fatalf("replace while a reader holds the file open: %v", err)
	}
	if !c.now.IsZero() {
		t.Error("the replace waited on the reader, want no wait")
	}
	if old, err := io.ReadAll(held); err != nil || string(old) != "old" {
		t.Errorf("the held handle reads %q, %v, want the old content", old, err)
	}
	if got, err := readFileShared(path); err != nil || string(got) != "new" {
		t.Errorf("readFileShared = %q, %v, want the new content", got, err)
	}
}
