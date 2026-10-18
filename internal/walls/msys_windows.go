package walls

import "strings"

// msysDrive is the Windows path of an MSYS drive path: /d/Projects/x is
// D:\Projects\x. Git Bash shows every drive under /<letter>, and a command
// line from its tool names paths that way. A path whose first element is not a
// single letter is no drive path.
func msysDrive(p string) (string, bool) {
	if len(p) < 2 || p[0] != '/' || !isLetter(p[1]) || len(p) > 2 && p[2] != '/' {
		return "", false
	}
	rest := strings.ReplaceAll(p[2:], "/", `\`)
	return strings.ToUpper(p[1:2]) + `:\` + strings.TrimPrefix(rest, `\`), true
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
