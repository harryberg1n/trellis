package install

import (
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestOwned_AnotherSpellingOfTheStateRoot(t *testing.T) {
	root := t.TempDir()
	testiso.Write(t, BinPath(root, "v0.0.2", ""), "x")
	alias := testiso.AltSpelling(t, root)

	got, ok := Owned(Command(BinPath(root, "v0.0.2", ""), "Stop"), alias)
	if !ok || got != "v0.0.2" {
		t.Errorf("Owned through another spelling of the root = %q, %v; want v0.0.2", got, ok)
	}
	if _, ok := Owned(Command(BinPath(root, "v0.0.2", ""), "Stop"), t.TempDir()); ok {
		t.Error("a different root owned the hook")
	}
}

func TestCommand_QuotesTheAbsolutePathAndNamesTheEvent(t *testing.T) {
	got := Command(filepath.FromSlash("/state/bin/v0.0.1/trellis"), "PreToolUse")
	if want := `'/state/bin/v0.0.1/trellis' hook PreToolUse`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	spaced := Command(filepath.FromSlash("/my state/bin/v1/trellis"), "Stop")
	if want := `'/my state/bin/v1/trellis' hook Stop`; spaced != want {
		t.Errorf("got %s, want %s", spaced, want)
	}
}

func TestOwned_OnlyPinnedBinariesUnderTheStateRoot(t *testing.T) {
	root := filepath.FromSlash("/state")
	cases := []struct {
		command string
		version string
		ok      bool
	}{
		{`"/state/bin/v0.0.1/trellis" hook Stop`, "v0.0.1", true},
		{`"/state/bin/dev/trellis.exe" hook Stop`, "dev", true},
		{`"/other/bin/v0.0.1/trellis" hook Stop`, "", false},
		{`"/state/bin/v0.0.1/other" hook Stop`, "", false},
		{`"/state/bin/trellis" hook Stop`, "", false},
		{`/state/bin/v0.0.1/trellis hook Stop`, "", false},
		{`trellis hook Stop`, "", false},
		{``, "", false},
	}
	for _, c := range cases {
		v, ok := Owned(c.command, root)
		if v != c.version || ok != c.ok {
			t.Errorf("Owned(%s) = %q, %v; want %q, %v", c.command, v, ok, c.version, c.ok)
		}
	}
}

func TestBinPath_KeepsTheExecutableSuffix(t *testing.T) {
	root := filepath.FromSlash("/state")
	if got, want := BinPath(root, "v1.2.3", ".exe"), filepath.FromSlash("/state/bin/v1.2.3/trellis.exe"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := BinPath(root, "v1.2.3", ""), filepath.FromSlash("/state/bin/v1.2.3/trellis"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCommand_QuotesAPathForTheShellWithSingleQuotes(t *testing.T) {
	cases := []struct{ bin, want string }{
		{"/s/bin/v/trellis", `'/s/bin/v/trellis' hook Stop`},
		{"/state dir/$x/`id`/bin/v/trellis", "'/state dir/$x/`id`/bin/v/trellis' hook Stop"},
		{`/st"ate/trellis`, `'/st"ate/trellis' hook Stop`},
		{"/it's/bin/v/trellis", `'/it'\''s/bin/v/trellis' hook Stop`},
	}
	for _, c := range cases {
		if got := Command(filepath.FromSlash(c.bin), "Stop"); got != c.want {
			t.Errorf("Command(%q) = %s, want %s", c.bin, got, c.want)
		}
	}
}

func TestExecutable_ReadsWhatCommandWroteAndTheOlderDoubleQuotedForm(t *testing.T) {
	for _, bin := range []string{"/s/bin/v/trellis", "/state dir/$x/bin/v/trellis", "/it's/a'b/bin/v/trellis"} {
		got, ok := executable(Command(filepath.FromSlash(bin), "Stop"))
		if want := filepath.Clean(filepath.FromSlash(bin)); !ok || got != want {
			t.Errorf("executable(Command(%q)) = %q, %v; want %q", bin, got, ok, want)
		}
	}
	got, ok := executable(`"/s/bin/v/trellis" hook Stop`)
	if want := filepath.Clean(filepath.FromSlash("/s/bin/v/trellis")); !ok || got != want {
		t.Errorf("a double-quoted command from an older install = %q, %v; want %q", got, ok, want)
	}
	for _, bad := range []string{"trellis hook Stop", `'unterminated hook Stop`, `'' hook Stop`} {
		if _, ok := executable(bad); ok {
			t.Errorf("executable(%q) reported ours", bad)
		}
	}
}
