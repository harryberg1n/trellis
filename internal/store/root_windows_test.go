package store

import (
	"path/filepath"
	"testing"
)

func TestRoot_DefaultIsLocalAppData(t *testing.T) {
	t.Setenv("TRELLIS_STATE", "")
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	got, err := Root(filepath.Join(t.TempDir(), "repo"))
	if want := filepath.Join(base, "trellis"); err != nil || got != want {
		t.Fatalf("Root = %q, %v; want %q", got, err, want)
	}
}
