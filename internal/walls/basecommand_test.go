package walls

import "testing"

// The .exe strip is on every system, with its case; the fold of the rest of the
// name is Windows-only (see foldName).
func TestBaseCommand_StripsExeEverywhere(t *testing.T) {
	for in, want := range map[string]string{"/usr/bin/sed": "sed", "sed.exe": "sed", "Git.EXE": "git", `C:\x\Git.exe`: "git"} {
		if got := baseCommand(in); got != want {
			t.Errorf("baseCommand(%q) = %q, want %q", in, got, want)
		}
	}
}
