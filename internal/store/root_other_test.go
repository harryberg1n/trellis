//go:build !windows

package store

import (
	"path/filepath"
	"testing"
)

func TestRoot_DefaultHonoursXDGThenHome(t *testing.T) {
	t.Setenv("TRELLIS_STATE", "")
	repo := filepath.Join(t.TempDir(), "repo")
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	if got, err := Root(repo); err != nil || got != filepath.Join(xdg, "trellis") {
		t.Fatalf("with XDG: Root = %q, %v", got, err)
	}
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".local", "state", "trellis")
	if got, err := Root(repo); err != nil || got != want {
		t.Fatalf("with HOME: Root = %q, %v; want %q", got, err, want)
	}
}
