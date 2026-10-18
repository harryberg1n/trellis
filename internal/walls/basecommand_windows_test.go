//go:build windows

package walls

import "testing"

// Only Windows splits a path at a backslash; a Windows path never reaches a
// Linux hook.
func TestBaseCommand_ReadsAWindowsPathAndFoldsItsCase(t *testing.T) {
	for in, want := range map[string]string{`C:\x\Git.exe`: "git", `C:\Program Files\Git\cmd\GIT`: "git"} {
		if got := baseCommand(in); got != want {
			t.Errorf("baseCommand(%q) = %q, want %q", in, got, want)
		}
	}
}
