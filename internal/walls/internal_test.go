package walls

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSkipsVerification(t *testing.T) {
	cases := []struct {
		verb string
		args string
		want bool
	}{
		{"commit", "--no-verify -m x", true},
		{"commit", "-m x --no-verify", true},
		{"commit", "--no-ver -m x", true},
		{"commit", "-n -m x", true},
		{"commit", "-nm x", true},
		{"commit", "-an", true},
		{"commit", "-m --no-verify", false},
		{"commit", "-am -n", false},
		{"commit", "-m x", false},
		{"commit", "--amend --no-edit", false},
		{"commit", "-- --no-verify", false},
		{"push", "--no-verify origin main", true},
		{"push", "-n origin", false},
		{"merge", "--no-verify topic", true},
	}
	for _, c := range cases {
		call := gitCall{Verb: c.verb, Args: strings.Fields(c.args)}
		if got := skipsVerification(call); got != c.want {
			t.Errorf("git %s %s: skipsVerification = %v, want %v", c.verb, c.args, got, c.want)
		}
	}
}

func TestDiscardIntent(t *testing.T) {
	cases := []struct {
		args  string
		form  string
		paths []string
	}{
		{"reset --hard", "reset --hard", nil},
		{"reset --merge", "reset --merge", nil},
		{"reset --soft HEAD~1", "", nil},
		{"reset", "", nil},
		{"checkout -- a b", "checkout -- <paths>", []string{"a", "b"}},
		{"checkout .", "checkout -- <paths>", []string{"."}},
		{"checkout -f", "checkout -f", nil},
		{"checkout main", "", nil},
		{"restore a", "restore <paths>", []string{"a"}},
		{"restore --staged a", "", nil},
		{"restore -s HEAD~1 a", "restore <paths>", []string{"a"}},
		{"restore --source HEAD~1 a b", "restore <paths>", []string{"a", "b"}},
		{"restore --source=HEAD~1 a", "restore <paths>", []string{"a"}},
		{"restore -SW a", "restore <paths>", []string{"a"}},
		{"clean -fd", "clean -fd", nil},
		{"clean -nfd", "", nil},
		{"clean -n", "", nil},
		{"stash drop", "stash drop", nil},
		{"stash clear", "stash clear", nil},
		{"stash push", "", nil},
		{"branch -D x", "branch -D x", nil},
		{"branch -fD x", "branch -D x", nil},
		{"branch -d -f x", "branch -D x", nil},
		{"branch -d x", "", nil},
		{"worktree remove -f ../w", "worktree remove --force", []string{"../w"}},
		{"worktree remove ../w", "", nil},
		{"worktree list", "", nil},
	}
	for _, c := range cases {
		form, paths, ok := discardIntent(strings.Fields(c.args))
		if form != c.form || ok != (c.form != "") || !reflect.DeepEqual(paths, c.paths) {
			t.Errorf("git %s: (%q, %q, %v), want (%q, %q)", c.args, form, paths, ok, c.form, c.paths)
		}
	}
}

func TestParseStatus(t *testing.T) {
	out := " M a.txt\x00M  b.txt\x00MM c.txt\x00?? d.txt\x00!! e.log\x00R  new.txt\x00old.txt\x00 D f.txt\x00"
	got := parseStatus([]byte(out))
	want := cost{Unstaged: 3, Staged: 3, Untracked: 1}
	if got != want {
		t.Errorf("parseStatus = %+v, want %+v (a.txt, c.txt, f.txt unstaged; b.txt, c.txt, the rename staged; d.txt untracked)", got, want)
	}
}

func TestWriteTargets(t *testing.T) {
	dir := filepath.FromSlash("/work/lane")
	abs := func(p string) string {
		return filepath.Clean(filepath.Join(filepath.VolumeName(dir)+string(filepath.Separator), p))
	}
	cases := []struct {
		cmd  string
		want []string
	}{
		{"echo hi > out.txt", []string{filepath.Join(dir, "out.txt")}},
		{"echo hi >> /abs/log", []string{abs("/abs/log")}},
		{"echo hi 2> err.txt", []string{filepath.Join(dir, "err.txt")}},
		{"make 2>&1", nil},
		{"echo hi > /dev/null", nil},
		{`echo "a > b"`, nil},
		{`echo a\>b`, nil},
		{"sed -i s/a/b/ f.go g.go", []string{filepath.Join(dir, "f.go"), filepath.Join(dir, "g.go")}},
		{"sed s/a/b/ f.go", nil},
		{"tee a.txt b.txt", []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")}},
		{"cp src dst", []string{filepath.Join(dir, "dst")}},
		{"mv -t out a b", []string{filepath.Join(dir, "out")}},
		{"Set-Content -Path x.txt -Value hi", []string{filepath.Join(dir, "x.txt")}},
		{"echo hi > $OUT", nil},
		{"cd sub && echo hi > f", []string{filepath.Join(dir, "sub", "f")}},
		{"cat <<EOF\nx > y\nEOF", nil},
	}
	for _, c := range cases {
		var got []string
		walk("Bash", c.cmd, dir, false, func(seg []word, d string) { got = append(got, writeTargets(seg, d)...) })
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: write targets %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestParseGit_ReadsGlobalOptionsOutOfTheWay(t *testing.T) {
	seg := segments(`FOO=1 git -C sub -c core.hooksPath=/h --no-pager commit -m "x y"`)[0]
	got, ok := parseGit(seg, filepath.FromSlash("/w"))
	want := gitCall{Dir: filepath.FromSlash("/w/sub"), Globals: []string{"core.hooksPath=/h"}, Verb: "commit", Args: []string{"-m", "x y"}}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("parseGit = %+v, %v; want %+v", got, ok, want)
	}
	if _, ok := parseGit(segments("echo git commit")[0], "/w"); ok {
		t.Error("echo git commit read as a git call")
	}
}

func TestTell(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"fix the parser", ""},
		{"fix\n\nco-authored-by: A <a@b>", "an attribution trailer (co-authored-by)"},
		{"Generated with [Claude Code]", `an attribution footer ("Generated with")`},
		{"ask the assistant, Opus", `a tool or model name ("Opus")`},
		{"opusculum", ""},
		{"coauthored by nobody", ""},
	}
	for _, c := range cases {
		if got := tell(c.text); got != c.want {
			t.Errorf("tell(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
