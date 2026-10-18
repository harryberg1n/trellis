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
	dir := filepath.FromSlash("/work/tree")
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
	seg := segments("Bash", `FOO=1 git -C sub -c core.hooksPath=/h --no-pager commit -m "x y"`)[0]
	got, ok := parseGit(seg, filepath.FromSlash("/w"))
	want := gitCall{Dir: filepath.FromSlash("/w/sub"), Globals: []string{"core.hooksPath=/h"}, Verb: "commit", Args: []string{"-m", "x y"}}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("parseGit = %+v, %v; want %+v", got, ok, want)
	}
	if _, ok := parseGit(segments("Bash", "echo git commit")[0], "/w"); ok {
		t.Error("echo git commit read as a git call")
	}
}

func TestTokenize_PerTool(t *testing.T) {
	cases := []struct {
		name, tool, cmd string
		want            [][]string
	}{
		{"powershell keeps backslashes", "PowerShell", `git -C D:\Projects\trellis push`, [][]string{{"git", "-C", `D:\Projects\trellis`, "push"}}},
		{"bash escapes them", "Bash", `git -C D:\Projects\trellis push`, [][]string{{"git", "-C", "D:Projectstrellis", "push"}}},
		{"powershell backtick escape", "PowerShell", "echo a`;b", [][]string{{"echo", "a;b"}}},
		{"powershell doubled quote", "PowerShell", `echo 'it''s' "say ""x"""`, [][]string{{"echo", "it's", `say "x"`}}},
		{"powershell here-string", "PowerShell", "echo @\"\nline one\nline two\n\"@ tail", [][]string{{"echo", "line one\nline two\n", "tail"}}},
		{"comment hides a separator", "Bash", "git status # note; git reset --hard", [][]string{{"git", "status"}}},
		{"quoted separator", "Bash", `echo "a; b" && ls`, [][]string{{"echo", "a; b"}, {"ls"}}},
		{"escaped separator", "Bash", `echo a\;b; ls`, [][]string{{"echo", "a;b"}, {"ls"}}},
		{"redirect ampersand", "Bash", `make 2>&1 | tee log`, [][]string{{"make", "2>&1"}, {"tee", "log"}}},
		{"newline inside a message", "Bash", "git commit -m \"a\nb\"\nls", [][]string{{"git", "commit", "-m", "a\nb"}, {"ls"}}},
		{"powershell comment", "PowerShell", "git status <# ; git reset --hard #>; ls", [][]string{{"git", "status"}, {"ls"}}},
	}
	for _, c := range cases {
		var got [][]string
		for _, seg := range segments(c.tool, c.cmd) {
			got = append(got, texts(seg))
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: segments = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestGitCalls_InsideShellConstructs(t *testing.T) {
	cases := []struct {
		name, cmd string
		verbs     []string
		bypass    bool
	}{
		{"subshell", `(git commit --no-verify)`, []string{"commit"}, true},
		{"brace group", `{ git commit --no-verify; }`, []string{"commit"}, true},
		{"if", `if true; then git commit --no-verify; fi`, []string{"commit"}, true},
		{"negation", `! git commit --no-verify`, []string{"commit"}, true},
		{"for loop", `for f in a; do git reset --hard; done`, []string{"reset"}, false},
		{"bash -lc", `bash -lc 'git commit --no-verify'`, []string{"commit"}, true},
		{"sh -ec", `sh -ec "git commit --no-verify"`, []string{"commit"}, true},
		{"eval", `eval "git commit --no-verify"`, []string{"commit"}, true},
		{"env -i", `env -i git commit --no-verify`, []string{"commit"}, true},
		{"env -u NAME", `env -u FOO BAR=1 git commit --no-verify`, []string{"commit"}, true},
		{"sudo -u", `sudo -u x git commit --no-verify`, []string{"commit"}, true},
		{"sudo bash -c", `sudo bash -c 'git commit --no-verify'`, []string{"commit"}, true},
		{"xargs", `xargs git commit --no-verify`, []string{"commit"}, true},
		{"substitution in double quotes", `echo "$(git commit --no-verify)"`, []string{"commit"}, true},
		{"substitution in single quotes is data", `echo '$(git commit --no-verify)'`, nil, false},
		{"ansi-c quote with an escaped quote", `git commit -m $'it\'s' --no-verify`, []string{"commit"}, true},
		{"ansi-c quote alone is no bypass", `git commit -m $'it\'s --no-verify'`, []string{"commit"}, false},
		{"quoted parenthesis is data", `git commit -m '(x)' -m "{ y; }"`, []string{"commit"}, false},
		{"plain substitution counted once", `echo $(git commit --no-verify)`, []string{"commit"}, true},
		{"nested depth", `(if true; then bash -c "git push --no-verify"; fi)`, []string{"push"}, true},
		{"while", `while true; do git commit -n; done`, []string{"commit"}, true},
	}
	for _, c := range cases {
		a := &analysis{tool: "Bash", cwd: t.TempDir(), command: c.cmd}
		var verbs []string
		for _, call := range a.gitCalls() {
			verbs = append(verbs, call.Verb)
		}
		if !reflect.DeepEqual(verbs, c.verbs) {
			t.Errorf("%s: %q: git verbs %q, want %q", c.name, c.cmd, verbs, c.verbs)
		}
		if got := hookBypass(a) != ""; got != c.bypass {
			t.Errorf("%s: %q: hook-bypass deny = %v, want %v", c.name, c.cmd, got, c.bypass)
		}
	}
}
