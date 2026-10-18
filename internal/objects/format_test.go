package objects

import (
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/testsupport"
)

func TestFormat(t *testing.T) {
	cases := []struct {
		name, config, want string
		wantErr            bool
	}{
		{"no config", "", "sha1", false},
		{"no extensions", "[core]\n\trepositoryformatversion = 0\n", "sha1", false},
		{"sha256", "[core]\n\trepositoryformatversion = 1\n[extensions]\n\tobjectformat = sha256\n", "sha256", false},
		{"explicit sha1", "[extensions]\n\tobjectformat = sha1\n", "sha1", false},
		{"case and spacing", "[Extensions]\n  ObjectFormat=SHA256 ; trailing\n", "sha256", false},
		{"other section", "[remote \"x\"]\n\tobjectformat = sha256\n", "sha1", false},
		{"unknown", "[extensions]\n\tobjectformat = md5\n", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.config != "" {
				testsupport.Write(t, filepath.Join(dir, "config"), c.config)
			}
			got, err := Format(dir)
			if (err != nil) != c.wantErr || got != c.want {
				t.Fatalf("Format = %q, %v; want %q (err %v)", got, err, c.want, c.wantErr)
			}
		})
	}
}

func TestFormat_WorktreeReadsCommonDirConfig(t *testing.T) {
	root := t.TempDir()
	common := filepath.Join(root, "main", ".git")
	testsupport.Write(t, filepath.Join(common, "config"), "[extensions]\n\tobjectformat = sha256\n")
	wt := filepath.Join(common, "worktrees", "w")
	testsupport.Write(t, filepath.Join(wt, "commondir"), "../..\n")
	got, err := Format(wt)
	if err != nil || got != "sha256" {
		t.Fatalf("Format(worktree) = %q, %v; want sha256", got, err)
	}
}
