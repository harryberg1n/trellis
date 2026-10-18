package hook

import (
	"path/filepath"

	"github.com/harryberg1n/trellis/internal/objects"
)

// Cwd is the directory a hook run is about: the payload's cwd, else the
// process's working directory, "" when neither is known. The one value the
// off-here check and the repository lookup both start from.
func Cwd(raw []byte, wd func() (string, error)) string {
	if cwd := PayloadCwd(raw); cwd != "" {
		return cwd
	}
	cwd, err := wd()
	if err != nil {
		return ""
	}
	return cwd
}

// Off reports whether trellis is off for a hook run in cwd (see Cwd): the hook then exits
// 0 with no output, opens no store and writes no file. A repository is on once
// objects.MarkerFile is committed at its root, found by walking up from cwd
// to the first .git, a linked worktree's file included. Outside any repository,
// without the marker, and under TRELLIS_OFF=1 (off is that variable's value),
// trellis is off. It reads the filesystem only to stat; it creates nothing.
//
// Follows aphrollo-tools internal/cli/silence.go holdsTrellis and
// internal/compat/requires.go RepoRoot at 12b3724e. What it encodes: two gates
// in one repository fire every event twice, so the committed marker file at the
// repository root is the one switch; the check walks the filesystem because a
// hook asks it on every call and a process per call is too slow.
func Off(cwd, off string) bool {
	if off == "1" || !filepath.IsAbs(cwd) {
		return true
	}
	root, err := objects.FindRoot(cwd)
	return err != nil || !objects.HasMarker(root)
}
