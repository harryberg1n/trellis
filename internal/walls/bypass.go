package walls

import (
	"fmt"
	"strings"
)

// hookVerbs are the git subcommands that run hooks.
var hookVerbs = map[string]bool{"commit": true, "merge": true, "push": true, "rebase": true, "cherry-pick": true, "revert": true}

// hookBypass is the hook-bypass wall (2026-09 bypass refusals): a commit or
// push that skips the repository's hooks or its signing. `--no-verify`, `git
// -c core.hooksPath=...` (a hook directory of the agent's choosing) and `git
// -c commit.gpgsign...` are the three doors. A hook the owner installed is a
// decision; the agent does not get to turn it off.
func hookBypass(a *analysis) string {
	for _, c := range a.gitCalls() {
		if !hookVerbs[c.Verb] {
			continue
		}
		for _, kv := range c.Globals {
			key, _, _ := strings.Cut(kv, "=")
			switch key = strings.ToLower(strings.TrimSpace(key)); {
			case key == "core.hookspath":
				return fmt.Sprintf("git -c %s changes the hook directory for git %s", kv, c.Verb)
			case strings.HasPrefix(key, "commit.gpgsign") && (c.Verb == "commit" || c.Verb == "push"):
				return fmt.Sprintf("git -c %s changes signing for git %s", kv, c.Verb)
			}
		}
		if skipsVerification(c) {
			return fmt.Sprintf("git %s --no-verify skips the repository's hooks", c.Verb)
		}
	}
	return ""
}

// takesValue are the options whose next argument is a value, not an option:
// `git commit -m --no-verify` commits with that message.
var takesValue = map[string]bool{
	"-m": true, "-F": true, "-C": true, "-c": true, "-t": true, "--message": true, "--file": true,
	"--author": true, "--date": true, "--template": true, "--cleanup": true, "-o": true, "--push-option": true,
	"--repo": true, "--receive-pack": true, "--exec": true,
}

// skipsVerification reports whether the call carries --no-verify (or git's
// unambiguous abbreviation of it), or, for a commit, the short -n.
func skipsVerification(c gitCall) bool {
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		switch {
		case a == "--":
			return false
		case takesValue[a]:
			i++
		case strings.HasPrefix(a, "--"):
			if noVerify := "--no-verify"; len(a) >= len("--no-ve") && strings.HasPrefix(noVerify, a) {
				return true
			}
		case c.Verb == "commit" && strings.HasPrefix(a, "-") && len(a) > 1:
			if clusterHas(a[1:], 'n') {
				return true
			}
			if clusterTakesValue(a[1:]) {
				// -m"msg" and -am "msg": the rest, or the next argument, is the value.
				if strings.IndexAny(a[1:], "mFCct") == len(a[1:])-1 {
					i++
				}
			}
		}
	}
	return false
}

// clusterHas reports whether the short-option cluster has option c before any
// option that takes the rest of the cluster as its value.
func clusterHas(cluster string, c rune) bool {
	for _, r := range cluster {
		if r == c {
			return true
		}
		if strings.ContainsRune("mFCct", r) {
			return false
		}
	}
	return false
}

func clusterTakesValue(cluster string) bool { return strings.ContainsAny(cluster, "mFCct") }
