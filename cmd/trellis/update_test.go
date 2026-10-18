package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdate_HelpUsesTheOneVoice(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"update", "--help"}, &out, &errb)
	const want = "usage: trellis update [--config-dir D] [--to VERSION] [--dry] [--allow-dev] [--forget-missing]\n"
	if got := out.String() + errb.String(); code != 0 || !strings.HasPrefix(got, want) {
		t.Errorf("trellis update --help: exit %d, output %q; want exit 0 starting with %q", code, got, want)
	}
}

func TestUpdatePaths_OutsideARepoAcceptsAStateRootUnderTheWorkingDirectory(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	state := filepath.Join(home, ".local", "state", "trellis")
	t.Setenv("TRELLIS_STATE", state)
	root, _, err := updatePaths()
	if err != nil || root != state {
		t.Fatalf("updatePaths = %q, %v; want %q: the home directory is not a repository", root, err, state)
	}
}
