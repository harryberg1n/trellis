//go:build !windows

package testiso

import "strconv"

const (
	shimName = "git"
	toolExt  = ""
)

func shimScript(realGit, counter string) string {
	return "#!/bin/sh\necho x >> '" + counter + "'\nexec '" + realGit + "' \"$@\"\n"
}

// toolScript is a program that writes stderr to its standard error and exits
// with code.
func toolScript(code int, stderr string) string {
	return "#!/bin/sh\necho '" + stderr + "' >&2\nexit " + strconv.Itoa(code) + "\n"
}
