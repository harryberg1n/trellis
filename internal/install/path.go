package install

import (
	"path/filepath"
	"strings"

	"github.com/harryberg1n/trellis/internal/store"
)

// binName is the pinned binary's name without its suffix.
const binName = "trellis"

// BinPath is where the pinned copy of the binary for a version lives:
// <root>/bin/<version>/trellis<ext>. ext is the running binary's suffix, ".exe"
// on Windows.
func BinPath(root, version, ext string) string {
	return filepath.Join(root, "bin", version, binName+ext)
}

// Command is the hook command for one event: the pinned binary's absolute
// path in single quotes, then `hook <event>`.
//
// Claude Code runs a hook command through its default shell: bash, which on
// Windows is Git Bash. The command is therefore bash syntax. Single quotes keep
// every byte of the path literal, a `$`, a backtick, a `"` or a backslash
// included; the one byte they cannot hold, a single quote, is written as close quote, backslash, quote, open quote.
// The path is written with forward slashes because bash would read a backslash
// as an escape; Windows accepts forward slashes in a path.
func Command(bin, event string) string {
	return shellQuote(filepath.ToSlash(bin)) + " hook " + event
}

// shellQuote is s as one word of a POSIX shell, in single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Owned reports whether a hook command is one trellis wrote, and for which
// version. The marker is the command's executable: a file called trellis
// (or trellis.exe) in <root>/bin/<version>/. Nothing else in a settings file
// is ever taken for ours.
func Owned(command, root string) (version string, ok bool) {
	bin, ok := executable(command)
	if !ok {
		return "", false
	}
	name := strings.TrimSuffix(filepath.Base(bin), filepath.Ext(bin))
	dir := filepath.Dir(bin)
	if name != binName || !sameDir(filepath.Dir(dir), filepath.Join(root, "bin")) {
		return "", false
	}
	return filepath.Base(dir), true
}

// executable is the quoted path a trellis command starts with: the single-quoted
// form Command writes, or the double-quoted form older installs wrote.
func executable(command string) (string, bool) {
	var path string
	var ok bool
	if rest, single := strings.CutPrefix(command, "'"); single {
		path, ok = unquoteSingle(rest)
	} else if rest, double := strings.CutPrefix(command, `"`); double {
		path, _, ok = strings.Cut(rest, `"`)
	}
	if !ok || path == "" {
		return "", false
	}
	return filepath.Clean(filepath.FromSlash(path)), true
}

// unquoteSingle reads the rest of a single-quoted word, after its opening
// quote, undoing the close-escape-open sequence of shellQuote, and reports whether it was closed.
func unquoteSingle(rest string) (string, bool) {
	var b strings.Builder
	for {
		i := strings.IndexByte(rest, '\'')
		if i < 0 {
			return "", false
		}
		b.WriteString(rest[:i])
		rest = rest[i+1:]
		if after, esc := strings.CutPrefix(rest, `\''`); esc {
			b.WriteByte('\'')
			rest = after
			continue
		}
		return b.String(), true
	}
}

// sameDir reports whether two paths name one directory: equal as written, or
// equal once symlinks and 8.3 short names are resolved. A settings file written
// under one spelling of the state root is still ours under another.
func sameDir(a, b string) bool {
	return a == b || store.Canonical(a) == store.Canonical(b)
}
