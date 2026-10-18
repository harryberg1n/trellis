package install

import (
	"path/filepath"
	"testing"
)

func TestCommand_QuotesTheAbsolutePathAndNamesTheEvent(t *testing.T) {
	got := Command(filepath.FromSlash("/state/bin/v0.0.1/trellis"), "PreToolUse")
	if want := `"/state/bin/v0.0.1/trellis" hook PreToolUse`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	spaced := Command(filepath.FromSlash("/my state/bin/v1/trellis"), "Stop")
	if want := `"/my state/bin/v1/trellis" hook Stop`; spaced != want {
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
