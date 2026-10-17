// Copied from aphrollo-tools internal/msys/anchor_windows_test.go at 12b3724e.
// Incident #1043: tests wrote into the real repository and the global git config.

//go:build windows

package testiso

import (
	"os/exec"
	"testing"
	"time"
)

// The anchor is a shell that stays alive until it is let go, so that the first
// MSYS process of a test run is one whose temp directory outlives the run.
func TestStartAnchor_HoldsAShellUntilReleased(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on the PATH, so no MSYS /tmp to protect") // skip-ok: the anchor is for boxes that have the shell tools
	}
	release, exited := startAnchor()

	select {
	case <-exited:
		t.Fatal("the anchor shell ended on its own")
	default: // the shell said it is up and waits on its stdin; nothing to wait for
	}
	release()
	select {
	case <-exited:
	case <-time.After(15 * time.Second): // failure bound only, never reached on a pass
		t.Fatal("the anchor shell outlived its release")
	}
}
