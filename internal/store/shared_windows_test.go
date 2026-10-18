//go:build windows

package store

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
)

// holdForDelete opens path the way a writer about to rename over it does: with
// delete access. Any later open that does not share delete now fails with a
// sharing violation, which is how a plain os.ReadFile or os.OpenFile in the
// store would block a rename.
func holdForDelete(t *testing.T, path string) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	h, err := windows.CreateFile(name, windows.DELETE|windows.SYNCHRONIZE, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(h) })
}

func TestCheckpointRead_OpensWithDeleteSharing(t *testing.T) {
	s, _, _ := open(t)
	if err := s.Locked(func(tx *Tx) error {
		return tx.Save(Checkpoint{LastSeq: 3, Trees: map[string]kernel.TreeState{"t": kernel.TreeOpen}})
	}); err != nil {
		t.Fatal(err)
	}
	holdForDelete(t, s.statePath())
	var got Checkpoint
	err := s.Locked(func(tx *Tx) error {
		var err error
		got, err = tx.Checkpoint()
		return err
	})
	if err != nil || got.LastSeq != 3 {
		t.Errorf("Checkpoint with a delete-access handle open = %+v, %v; want LastSeq 3", got, err)
	}
}

func TestAllowRead_OpensWithDeleteSharing(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "repo"), clock.Real())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Allow("main-commit"); err != nil {
		t.Fatal(err)
	}
	p, err := s.allowPath("main-commit")
	if err != nil {
		t.Fatal(err)
	}
	holdForDelete(t, p)
	if ok, err := s.valid("main-commit"); err != nil || !ok {
		t.Errorf("valid with a delete-access handle open = %v, %v; want true", ok, err)
	}
}

func TestToolVersionRead_OpensWithDeleteSharing(t *testing.T) {
	root := t.TempDir()
	at := time.Unix(100, 0)
	if err := SaveToolVersion(root, "/x/gitleaks", at, "8.21.1"); err != nil {
		t.Fatal(err)
	}
	holdForDelete(t, filepath.Join(root, toolVersionFile))
	if v, ok := LoadToolVersion(root, "/x/gitleaks", at); !ok || v != "8.21.1" {
		t.Errorf("LoadToolVersion with a delete-access handle open = %q, %v; want 8.21.1", v, ok)
	}
}

func TestSync_OpensWithDeleteSharing(t *testing.T) {
	s, _, _ := open(t)
	if err := s.Locked(func(tx *Tx) error { return tx.Save(Checkpoint{LastSeq: 1}) }); err != nil {
		t.Fatal(err)
	}
	holdForDelete(t, s.statePath())
	if err := s.Sync(); err != nil {
		t.Errorf("Sync with a delete-access handle open on state.json: %v", err)
	}
}
