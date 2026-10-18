package testsupport

const shimName = "git.bat"

func shimScript(realGit, counter string) string {
	return "@echo off\r\n>>\"" + counter + "\" echo x\r\n\"" + realGit + "\" %*\r\n"
}
