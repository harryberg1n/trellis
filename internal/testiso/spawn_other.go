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

// gitleaksScript answers `version` with version and logs every call; any other
// call prints the report file and exits with code.
func gitleaksScript(version, report, log string, code int) string {
	return "#!/bin/sh\nif [ \"$1\" = version ]; then echo version >> '" + log + "'; echo '" + version + "'; exit 0; fi\n" +
		"echo \"$@\" >> '" + log + "'\ncat '" + report + "'\nexit " + strconv.Itoa(code) + "\n"
}
