package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestToolVersion_IsKeptByBinaryPathAndModTime(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "gitleaks")
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	if _, ok := LoadToolVersion(root, bin, at); ok {
		t.Fatal("LoadToolVersion before any save found a version")
	}
	if err := SaveToolVersion(root, bin, at, "8.18.2"); err != nil {
		t.Fatal(err)
	}
	if v, ok := LoadToolVersion(root, bin, at); !ok || v != "8.18.2" {
		t.Errorf("LoadToolVersion = %q, %v; want 8.18.2", v, ok)
	}
	if _, ok := LoadToolVersion(root, bin, at.Add(time.Second)); ok {
		t.Error("a binary with another mtime reused the cached version")
	}
	if _, ok := LoadToolVersion(root, bin+"2", at); ok {
		t.Error("another binary path reused the cached version")
	}
	// A second binary does not displace the first.
	other := filepath.Join(root, "other", "gitleaks")
	if err := SaveToolVersion(root, other, at, "8.20.0"); err != nil {
		t.Fatal(err)
	}
	if v, ok := LoadToolVersion(root, bin, at); !ok || v != "8.18.2" {
		t.Errorf("first binary after a second was saved: %q, %v; want 8.18.2", v, ok)
	}
	// A reinstall (new mtime) is asked again and the answer replaces the old.
	if err := SaveToolVersion(root, bin, at.Add(time.Hour), "8.21.1"); err != nil {
		t.Fatal(err)
	}
	if v, ok := LoadToolVersion(root, bin, at.Add(time.Hour)); !ok || v != "8.21.1" {
		t.Errorf("after a reinstall: %q, %v; want 8.21.1", v, ok)
	}
}

func TestToolVersion_AnUnreadableFileIsNoAnswer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, toolVersionFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(100, 0)
	if _, ok := LoadToolVersion(root, "/x/gitleaks", at); ok {
		t.Fatal("a corrupt cache answered")
	}
	if err := SaveToolVersion(root, "/x/gitleaks", at, "8.19.0"); err != nil {
		t.Fatalf("save over a corrupt cache: %v", err)
	}
	if v, ok := LoadToolVersion(root, "/x/gitleaks", at); !ok || v != "8.19.0" {
		t.Errorf("after the save: %q, %v; want 8.19.0", v, ok)
	}
}
