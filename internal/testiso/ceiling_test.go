// Copied from aphrollo-tools internal/gitenv/sealed_test.go at 12b3724e.
// Incident #1043: tests wrote into the real repository and the global git config.

package testiso

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCeilingList_NamesEachDirOnceAndSkipsEmpty(t *testing.T) {
	// Resolved once: on a runner whose TEMP is an 8.3 short path, CeilingList
	// rightly names the long spelling too, which is not what this test is about.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	sep := string(os.PathListSeparator)

	if got, want := CeilingList(a, "", b), a+sep+b; got != want {
		t.Errorf("ceilingList = %q, want %q", got, want)
	}
	if got := CeilingList(""); got != "" {
		t.Errorf("ceilingList of nothing = %q, want empty", got)
	}
}

func TestCeilingList_AddsTheResolvedSpellingOfASymlinkedDir(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err) // skip-ok: an environment probe, symlinks need privilege on some platforms.
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}

	want := link + string(os.PathListSeparator) + resolved
	if got := CeilingList(link); got != want {
		t.Errorf("ceilingList = %q, want %q", got, want)
	}
}
