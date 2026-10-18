//go:build !windows

package testsupport

const shimName = "git"

func shimScript(realGit, counter string) string {
	return "#!/bin/sh\necho x >> '" + counter + "'\nexec '" + realGit + "' \"$@\"\n"
}
