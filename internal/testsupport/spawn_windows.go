package testsupport

import "strconv"

const shimName = "git.bat"

func shimScript(realGit, counter string) string {
	return "@echo off\r\n>>\"" + counter + "\" echo x\r\n\"" + realGit + "\" %*\r\n"
}

const toolExt = ".bat"

// toolScript is a program that writes stderr to its standard error and exits
// with code.
func toolScript(code int, stderr string) string {
	return "@echo off\r\necho " + stderr + " 1>&2\r\nexit /b " + strconv.Itoa(code) + "\r\n"
}
