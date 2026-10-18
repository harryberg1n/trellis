package hook

import (
	"path/filepath"

	"github.com/harryberg1n/trellis/internal/objects"
)

// Off reports whether trellis is off for the payload raw: the hook then exits
// 0 with no output, opens no store and writes no file. A repository is on once
// objects.MarkerFile is committed at its root, found by walking up from the
// payload's cwd (the process's working directory when the payload names none)
// to the first .git, a linked worktree's file included. Outside any repository,
// without the marker, and under TRELLIS_OFF=1 (off is that variable's value),
// trellis is off. It reads the filesystem only to stat; it creates nothing.
//
// Follows aphrollo-tools internal/cli/silence.go holdsTrellis and
// internal/compat/requires.go RepoRoot at 12b3724e. What it encodes: two gates
// in one repository fire every event twice, so the committed marker file at the
// repository root is the one switch; the check walks the filesystem because a
// hook asks it on every call and a process per call is too slow.
func Off(raw []byte, off string, wd func() (string, error)) bool {
	if off == "1" {
		return true
	}
	cwd := PayloadCwd(raw)
	if cwd == "" {
		var err error
		if cwd, err = wd(); err != nil {
			return true
		}
	}
	if !filepath.IsAbs(cwd) {
		return true
	}
	root, err := objects.FindRoot(cwd)
	return err != nil || !objects.HasMarker(root)
}
