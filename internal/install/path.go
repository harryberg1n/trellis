package install

import (
	"path/filepath"
	"strings"
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
// path in double quotes, forward slashes so no shell eats a backslash, then
// `hook <event>`.
func Command(bin, event string) string {
	return `"` + filepath.ToSlash(bin) + `" hook ` + event
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
	if name != binName || filepath.Dir(dir) != filepath.Join(root, "bin") {
		return "", false
	}
	return filepath.Base(dir), true
}

// executable is the quoted path a trellis command starts with.
func executable(command string) (string, bool) {
	rest, ok := strings.CutPrefix(command, `"`)
	if !ok {
		return "", false
	}
	path, _, ok := strings.Cut(rest, `"`)
	if !ok || path == "" {
		return "", false
	}
	return filepath.Clean(filepath.FromSlash(path)), true
}
