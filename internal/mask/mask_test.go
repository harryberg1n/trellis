package mask

import (
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestMain(m *testing.M) { testiso.Main(m) }

// blank is n spaces: the masked form of an n-byte string body.
func blank(n int) string { return strings.Repeat(" ", n) }

func TestBlank_Bash(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"double quoted token", `echo "git reset --hard"`, `echo "` + blank(16) + `"`},
		{"single quoted token", `echo 'a --no-verify'`, `echo '` + blank(13) + `'`},
		{"escaped quote stays inside", `echo "say \"x\" --force"`, `echo "` + blank(17) + `"`},
		{"comment word", "ls # git clean -f\nls", "ls " + blank(14) + "\nls"},
		{"hash inside a word is code", "echo a#b --x", "echo a#b --x"},
		{"code escape opens no string", `echo don\'t --hard`, `echo don\'t --hard`},
		{"newline survives in a string", "echo 'a\nb'", "echo ' \n '"},
	}
	for _, c := range cases {
		if got := Blank("Bash", c.in, true, true); got != c.want {
			t.Errorf("%s: Blank(Bash, %q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestBlank_PowerShell(t *testing.T) {
	cases := []struct{ name, tool, in, want string }{
		{"single quotes", "PowerShell", `Write-Host 'git reset --hard'`, `Write-Host '` + blank(16) + `'`},
		{"doubled quote stays inside", "PowerShell", `Write-Host 'it''s --force'`, `Write-Host '` + blank(13) + `'`},
		{"block comment", "PowerShell", `a <# git clean -f #> b`, "a " + blank(18) + " b"},
		{"backslash is not an escape", "PowerShell", `echo "C:\" --hard`, `echo "` + blank(3) + `" --hard`},
		{"bash for Bash", "Bash", `echo 'x'`, `echo ' '`},
	}
	for _, c := range cases {
		if got := Blank(c.tool, c.in, true, true); got != c.want {
			t.Errorf("%s: Blank(%s, %q) = %q, want %q", c.name, c.tool, c.in, got, c.want)
		}
	}
}

func TestCode_BlanksNewlinesInsideStringsOnly(t *testing.T) {
	cases := []struct{ name, tool, in, want string }{
		{"bash message", "Bash", "git commit -m \"a\nb\"\nls", "git commit -m \"   \"\nls"},
		{"bash comment keeps its terminator", "Bash", "ls # x\nls", "ls    \nls"},
		{"powershell here-string", "PowerShell", "@\"\nx\n\"@\nls", "@\"   \"@\nls"},
	}
	for _, c := range cases {
		if got := Code(c.tool, c.in); got != c.want {
			t.Errorf("%s: Code(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
