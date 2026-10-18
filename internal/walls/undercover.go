package walls

import (
	"os"
	"path/filepath"
	"strings"
)

// maxMessageFile bounds a message file the wall reads.
const maxMessageFile = 1 << 20

// undercover is the undercover policy (2026-09 owner): a commit message or PR
// body with an attribution trailer or footer, or the tool or model name. It is
// read from `git commit -m/-F` and `gh pr create/edit --body/--body-file`, at
// PreToolUse, before the text exists anywhere else.
func undercover(a *analysis) string {
	if !a.isShell() || !strings.Contains(a.command, "git") && !strings.Contains(a.command, "gh") {
		return ""
	}
	var cause string
	stdin := false
	walk(a.tool, a.command, a.cwd, true, func(seg []word, dir string) {
		if cause != "" {
			return
		}
		texts, fromStdin := messages(seg, dir)
		stdin = stdin || fromStdin
		for _, t := range texts {
			if why := tell(t); why != "" {
				cause = why
				return
			}
		}
	})
	if cause == "" && stdin {
		_, bodies := splitHeredocs(a.command)
		cause = tell(bodies)
	}
	if cause == "" {
		return ""
	}
	return "the text of this commit or pull request carries " + cause
}

// messages is the message text a git commit or gh pr create/edit segment
// carries, and whether it reads the rest from standard input (-F -).
func messages(seg []word, dir string) (out []string, stdin bool) {
	var args, valueFlags, fileFlags []string
	if c, ok := parseGit(seg, dir); ok && c.Verb == "commit" {
		args, valueFlags, fileFlags = c.Args, []string{"-m", "--message"}, []string{"-F", "--file"}
	} else if all := texts(seg); len(all) > 2 && baseCommand(all[0]) == "gh" && all[1] == "pr" && (all[2] == "create" || all[2] == "edit") {
		args, valueFlags, fileFlags = all[3:], []string{"-b", "--body"}, []string{"-F", "--body-file"}
	} else {
		return nil, false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch {
		case flagIs(a, valueFlags) && next != "":
			out = append(out, next)
		case flagIs(a, fileFlags):
			if next == "-" {
				stdin = true
			} else if next != "" {
				out = append(out, readMessage(dir, next))
			}
		case flagValue(a, valueFlags) != "":
			out = append(out, flagValue(a, valueFlags))
		case flagValue(a, fileFlags) == "-":
			stdin = true
		case flagValue(a, fileFlags) != "":
			out = append(out, readMessage(dir, flagValue(a, fileFlags)))
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.HasSuffix(a, "m") && len(a) > 2 && next != "":
			out = append(out, next) // a cluster such as -am "message"
		}
	}
	return out, stdin
}

func flagIs(a string, flags []string) bool {
	for _, f := range flags {
		if a == f {
			return true
		}
	}
	return false
}

// flagValue is the value of an attached flag: --message=x, or -mx for a short one.
func flagValue(a string, flags []string) string {
	for _, f := range flags {
		if strings.HasPrefix(f, "--") {
			if v, ok := strings.CutPrefix(a, f+"="); ok {
				return v
			}
		} else if len(a) > 2 && strings.HasPrefix(a, f) && !strings.HasPrefix(a, "--") {
			return a[2:]
		}
	}
	return ""
}

// readMessage is the text of a message file named relative to dir, or "".
func readMessage(dir, name string) string {
	p := resolve(dir, name)
	if p == "" {
		return ""
	}
	f, err := os.Open(filepath.Clean(p))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, maxMessageFile)
	n, _ := f.Read(buf)
	return string(buf[:n])
}
