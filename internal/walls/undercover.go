package walls

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// maxMessageFile bounds a message file the wall reads.
const maxMessageFile = 1 << 20

// undercover is the undercover policy (2026-09 owner): a commit message or PR
// body with an attribution trailer or footer, or the tool or model name. It is
// read from `git commit` and `git merge` -m/-F and from `gh pr create/edit/merge`
// --title/--subject/--body/--body-file (with a squash the title is the subject on
// main), at PreToolUse, before the text exists anywhere else.
func undercover(a *analysis) string {
	if !a.isShell() || !strings.Contains(foldName(a.command), "git") && !strings.Contains(foldName(a.command), "gh") {
		return ""
	}
	var cause, identity string
	stdin := false
	walk(a.tool, a.command, a.cwd, true, func(seg []word, dir string) {
		if cause != "" || identity != "" {
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
		identity = identityCause(seg, dir)
	})
	if cause == "" && identity == "" && stdin {
		_, bodies := splitHeredocs(a.command)
		cause = tell(bodies)
	}
	switch {
	case cause != "":
		return "the text of this commit or pull request carries " + cause
	case identity != "":
		return "the author or committer identity of this commit carries " + identity
	}
	return ""
}

// messages is the message text a git commit or gh pr create/edit segment
// carries, and whether it reads the rest from standard input (-F -). Source of
// the gh title, subject and merge reads: aphrollo-tools 12b3724e
// internal/tdd/postedit/undercoverbash.go (ghFlagCandidates, ghPostingVerbs),
// which reads the title and the squash subject because the title is the subject
// on main.
func messages(seg []word, dir string) (out []string, stdin bool) {
	var args, valueFlags, fileFlags []string
	if c, ok := parseGit(seg, dir); ok && (c.Verb == "commit" || c.Verb == "merge") {
		args, valueFlags, fileFlags = c.Args, []string{"-m", "--message"}, []string{"-F", "--file"}
	} else if all := texts(seg); len(all) > 2 && baseCommand(all[0]) == "gh" && all[1] == "pr" && (all[2] == "create" || all[2] == "edit" || all[2] == "merge") {
		args, valueFlags, fileFlags = all[3:], []string{"-b", "--body", "-t", "--title", "--subject"}, []string{"-F", "--body-file"}
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
		case slices.Contains(valueFlags, a) && next != "":
			out = append(out, next)
		case slices.Contains(fileFlags, a):
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

// identityKeys are the -c settings that name who a commit is by.
var identityKeys = map[string]bool{"user.name": true, "user.email": true}

// commitWriters are the git verbs that write a commit, and so have an identity
// to judge; `git log --author=` is a search, not an identity.
var commitWriters = map[string]bool{"commit": true, "merge": true, "cherry-pick": true, "revert": true, "am": true, "rebase": true, "pull": true}

// identityCause is the vendor address a git call sets as the identity of its
// commit, or "": `--author=<ident>`, `--author <ident>`, and `-c user.name=` or
// `-c user.email=` among the globals of the call. Source: aphrollo-tools
// 12b3724e internal/undercover/commit.go (CommitTell, which judges the author
// and committer of a commit), read here before the commit exists.
func identityCause(seg []word, dir string) string {
	c, ok := parseGit(seg, dir)
	if !ok || !commitWriters[c.Verb] {
		return ""
	}
	for _, kv := range c.Globals {
		key, value, _ := strings.Cut(kv, "=")
		if identityKeys[strings.ToLower(strings.TrimSpace(key))] {
			if why := identityTell(value); why != "" {
				return why
			}
		}
	}
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		if a == "--" {
			break
		}
		value, attached := strings.CutPrefix(a, "--author=")
		if a == "--author" && i+1 < len(c.Args) {
			value, attached = c.Args[i+1], true
			i++
		}
		if attached {
			if why := identityTell(value); why != "" {
				return why
			}
		}
	}
	return ""
}
