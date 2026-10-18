package testiso

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

// gitleaksScript answers `version` with version and logs every call; any other
// call prints the report file and exits with code.
func gitleaksScript(version, report, log string, code int) string {
	return "@echo off\r\nif \"%1\"==\"version\" (\r\n>>\"" + log + "\" echo version\r\necho " + version + "\r\nexit /b 0\r\n)\r\n" +
		">>\"" + log + "\" echo %*\r\ntype \"" + report + "\"\r\nexit /b " + strconv.Itoa(code) + "\r\n"
}
