package run

import "strings"

// GitArgv is a git argv split at its subcommand: git's own leading options,
// the subcommand and the arguments after it.
type GitArgv struct {
	// Dirs is the -C values in order.
	Dirs []string
	// Config is the -c values in order.
	Config []string
	// Verb is the subcommand; empty when the argv has none.
	Verb string
	// Rest is the arguments after the subcommand.
	Rest []string
}

// ParseGitArgs reads args (a git argv without the program name) up to its
// subcommand. It is the one reader of git's global options: every option that
// takes its value as the next argument is skipped with that value, so a value
// is never taken for the subcommand.
func ParseGitArgs(args []string) GitArgv {
	var g GitArgv
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			g.Verb = a
			g.Rest = args[i+1:]
			return g
		}
		if !takesValue(a) || i+1 >= len(args) {
			continue
		}
		i++
		switch a {
		case "-C":
			g.Dirs = append(g.Dirs, args[i])
		case "-c":
			g.Config = append(g.Config, args[i])
		}
	}
	return GitArgv{}
}

func takesValue(opt string) bool {
	switch opt {
	case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env":
		return true
	}
	return false
}
