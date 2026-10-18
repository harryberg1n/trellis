package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestWriteSettings_KeepsASymlinkedSettingsFileAndWritesThroughIt(t *testing.T) {
	f := newFixture(t)
	shared := filepath.Join(t.TempDir(), "shared-settings.json")
	testiso.Write(t, shared, foreignSettings)
	if err := os.MkdirAll(filepath.Dir(f.settings()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, f.settings()); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}

	if _, err := Install(f.opts("1.0.0", false)); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(f.settings())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the settings link was replaced by a file")
	}
	got := readFile(t, shared)
	if !strings.Contains(got, `hook SessionStart`) || !strings.Contains(got, "foreign") {
		t.Errorf("link target = %q, want the trellis hooks beside the foreign ones", got)
	}
}

func TestWriteSettings_ResolvesASymlinkedClaudeDir(t *testing.T) {
	f := newFixture(t)
	realDir := filepath.Join(t.TempDir(), "claude-shared")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(f.repo, ".claude")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}

	if err := writeSettings(f.repo, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(realDir, "settings.local.json")); got != "{}\n" {
		t.Errorf("file in the link target = %q, want {}", got)
	}
}

func TestWriteSettings_PublishesThroughATempFileInTheSameDirectory(t *testing.T) {
	f := newFixture(t)
	testiso.Write(t, f.settings(), "old\n")
	var from, to string
	err := writeSettingsWith(f.repo, []byte("new\n"), store.WithRename(func(oldpath, newpath string) error {
		from, to = oldpath, newpath
		return errors.New("rename refused")
	}))

	if err == nil {
		t.Fatal("err = nil, want the refused rename")
	}
	if filepath.Dir(from) != filepath.Dir(f.settings()) || to != f.settings() || from == to {
		t.Errorf("rename %s -> %s, want a temp file beside %s", from, to, f.settings())
	}
	if got := readFile(t, f.settings()); got != "old\n" {
		t.Errorf("settings = %q after a failed write, want the old file untouched", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(f.settings()))
	if len(entries) != 1 {
		t.Errorf("%d files left in .claude, want only settings.local.json", len(entries))
	}
}
