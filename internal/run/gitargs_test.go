package run

import (
	"reflect"
	"testing"
)

func TestParseGitArgs(t *testing.T) {
	cases := []struct {
		args []string
		want GitArgv
	}{
		{[]string{"status"}, GitArgv{Verb: "status", Rest: []string{}}},
		{
			[]string{"-C", "d", "-c", "k=v", "--no-pager", "commit", "-m", "x"},
			GitArgv{Dirs: []string{"d"}, Config: []string{"k=v"}, Verb: "commit", Rest: []string{"-m", "x"}},
		},
		{[]string{"--config-env", "foo=BAR", "reset", "--hard"}, GitArgv{Verb: "reset", Rest: []string{"--hard"}}},
		{[]string{"--super-prefix", "sub/", "--git-dir=x", "log"}, GitArgv{Verb: "log", Rest: []string{}}},
		{[]string{"-C"}, GitArgv{}},
		{nil, GitArgv{}},
	}
	for _, c := range cases {
		if got := ParseGitArgs(c.args); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseGitArgs(%q) = %+v, want %+v", c.args, got, c.want)
		}
	}
}

func TestKind_ReadsGitExeAndEveryGlobalOption(t *testing.T) {
	cases := []struct {
		spec Spec
		want string
	}{
		{Spec{Name: "git.exe", Args: []string{"-C", "d", "status"}}, "status"},
		{Spec{Name: "git", Args: []string{"--config-env", "a=B", "fetch"}}, "fetch"},
		{Spec{Name: "git"}, "git"},
		{Spec{Name: "gitleaks", Args: []string{"detect"}}, "gitleaks"},
	}
	for _, c := range cases {
		if got := Kind(c.spec); got != c.want {
			t.Errorf("Kind(%+v) = %q, want %q", c.spec, got, c.want)
		}
	}
}
