package tree

// shellCommand is the program and arguments that run cmd as one shell line.
func shellCommand(cmd string) (name string, args []string) {
	return "cmd", []string{"/C", cmd}
}
