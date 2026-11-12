package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, text string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "trellis.toml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoad_NoFileAndAnEmptyFileAreTheDefaults(t *testing.T) {
	want := Config{Tree: Tree{Remote: "origin", New: New{Place: "sibling"}, Merge: Merge{Checks: "auto"}}}
	for _, dir := range []string{t.TempDir(), write(t, ""), write(t, "# trellis is on\n")} {
		got, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Load = %+v, want %+v", got, want)
		}
	}
}

func TestLoad_ReadsEveryKey(t *testing.T) {
	dir := write(t, `# threadspyp
[tree]
remote = "github"   # the GitHub remote is not origin here

[tree.new]
link = ["node_modules", 'frontend/node_modules']
run = [
  "npm ci",   # the lockfile changed
  "make warm",
]

[tree.merge]
require = ["test-linux", "test-windows"]
`)
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Tree: Tree{
		Remote: "github",
		New:    New{Link: []string{"node_modules", "frontend/node_modules"}, Run: []string{"npm ci", "make warm"}, Place: "sibling"},
		Merge:  Merge{Checks: "auto", Require: []string{"test-linux", "test-windows"}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load =\n %+v\nwant\n %+v", got, want)
	}
}

func TestLoad_ChecksNone(t *testing.T) {
	got, err := Load(write(t, "[tree.merge]\nchecks = \"none\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Tree.Merge.Checks != "none" {
		t.Errorf("checks = %q, want none", got.Tree.Merge.Checks)
	}
}

// Feedback #65: a tree inside the primary checkout loads the repository's
// nested instruction files; beside it (the default) it loads none.
func TestLoad_PlaceIsSiblingUnlessItSaysInside(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"", "sibling"},
		{"[tree.new]\nplace = \"sibling\"\n", "sibling"},
		{"[tree.new]\nplace = \"inside\"\n", "inside"},
	} {
		got, err := Load(write(t, c.text))
		if err != nil {
			t.Fatal(err)
		}
		if got.Tree.New.Place != c.want {
			t.Errorf("%q: place = %q, want %q", c.text, got.Tree.New.Place, c.want)
		}
	}
}

func TestLoad_RefusesWhatItDoesNotKnow(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"[tree.new]\nplace = \"beside\"\n", `trellis.toml:2: tree.new.place: "sibling" or "inside"`},
		{"[tree.new]\nlnk = [\"x\"]\n", `trellis.toml:2: unknown key tree.new.lnk`},
		{"[tree.nwe]\n", `trellis.toml:1: unknown table [tree.nwe]`},
		{"remote = \"x\"\n", `trellis.toml:1: unknown key remote`},
		{"[tree]\nremote = github\n", `trellis.toml:2: tree.remote: a quoted string is needed`},
		{"[tree.new]\nlink = \"node_modules\"\n", `trellis.toml:2: tree.new.link: a list of quoted strings is needed`},
		{"[tree.merge]\nchecks = \"some\"\n", `trellis.toml:2: tree.merge.checks: "auto" or "none"`},
		{"[tree.merge]\nchecks = \"none\"\nrequire = [\"ci\"]\n", `trellis.toml: tree.merge: checks = "none" and require exclude each other`},
		{"[tree.new]\nrun = [\"a\",\n", `trellis.toml:2: tree.new.run: the list is not closed`},
		{"[tree]\nremote = \"a\"\nremote = \"b\"\n", `trellis.toml:3: tree.remote is set twice`},
		{"[tree.new]\nlink = [\"../outside\"]\n", `trellis.toml:2: tree.new.link: "../outside" is not a path inside the checkout`},
	} {
		_, err := Load(write(t, c.text))
		if err == nil || err.Error() != c.want {
			t.Errorf("%q: err = %v, want %q", c.text, err, c.want)
		}
	}
}
