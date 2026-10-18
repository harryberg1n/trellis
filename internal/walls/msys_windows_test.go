package walls

import "testing"

func TestResolve_ReadsAnMSYSDrivePathOnWindows(t *testing.T) {
	cases := []struct{ cwd, p, want string }{
		{`C:\w`, "/d/Projects/x", `D:\Projects\x`},
		{`C:\w`, "/D/Projects/x", `D:\Projects\x`},
		{`C:\w`, "/c", `C:\`},
		{`C:\w`, "/dev/null", ""},
		{`C:\w`, "/tmp/x", `C:\tmp\x`},
		{`C:\w`, "/dd/x", `C:\dd\x`},
	}
	for _, c := range cases {
		if got := resolve(c.cwd, c.p); got != c.want {
			t.Errorf("resolve(%q, %q) = %q, want %q", c.cwd, c.p, got, c.want)
		}
	}
}

func TestAllowPeople_FindsThePinnedWindowsBinaryInDoubleQuotes(t *testing.T) {
	cmd := `"C:\Users\x\AppData\Local\trellis\bin\v0.0.6\trellis.exe" allow y`
	a := &analysis{tool: "Bash", cwd: t.TempDir(), command: cmd}
	if got, want := allowPeople(a), "allow is for a person: ask them to run trellis allow y"; got != want {
		t.Errorf("allowPeople(%q) = %q, want %q", cmd, got, want)
	}
}
