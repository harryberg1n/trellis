package walls

import (
	"slices"
	"strings"

	"github.com/harryberg1n/trellis/internal/objects"
)

// pushValueOptions are git push's options that take their value as the next
// argument when it is not attached with "=".
var pushValueOptions = []string{"--push-option", "--repo", "--receive-pack", "--exec"}

// pushSparesDefault reports whether a push leaves the default branch alone on
// every remote: each ref it writes or deletes there is named on the command
// line and none of them is the default branch. Live denies on honest work,
// fanpyp 2026-10-25 and threadspyp 2026-11-04 (#50, #52): a push of a backup
// ref and `push origin --delete <branch>` from the primary were refused as
// main commits, and `gh api -X DELETE` went around the wall.
//
// What the command line does not settle keeps the wall standing: no refspec
// (push.default picks the current branch, which is the default), HEAD or @,
// --all, --branches, --mirror, --prune, a wildcard, a destination outside
// refs/ that names the default, and a bare <src> pushed to a remote whose
// config holds push refspecs (they map it to a destination unseen here).
func pushSparesDefault(r objects.Repo, args []string) bool {
	var operands []string
	var del, tags, dry bool
	afterDashes := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case afterDashes || !strings.HasPrefix(a, "-") || a == "-":
			operands = append(operands, a)
		case a == "--":
			afterDashes = true
		case a == "--all" || a == "--branches" || a == "--mirror" || a == "--prune":
			return false
		case a == "--delete":
			del = true
		case a == "--tags":
			tags = true
		case slices.Contains(pushValueOptions, a):
			i++
		case strings.HasPrefix(a, "--"):
			// --force-with-lease[=…], --set-upstream, --atomic and the like
			// change how a ref is written, not which.
		default: // a cluster of short options
			flags, value, found := strings.Cut(a[1:], "o") // -o's value is the rest, or the next argument
			if found && value == "" {
				i++
			}
			del = del || strings.Contains(flags, "d")
			dry = dry || strings.Contains(flags, "n") // -n is --dry-run, which harmless sees in its long form only
		}
	}
	if dry {
		return true
	}
	if len(operands) == 0 {
		return false
	}
	remote, refspecs := operands[0], operands[1:]
	if len(refspecs) == 0 {
		return tags // --tags alone pushes refs/tags/* and nothing else
	}
	if del {
		for _, ref := range refspecs {
			if namesDefault(r, ref) {
				return false
			}
		}
		return true
	}
	mapped := remoteHasPushRefspecs(r, remote)
	for i := 0; i < len(refspecs); i++ {
		spec := refspecs[i]
		if spec == "tag" && i+1 < len(refspecs) {
			i++ // `tag <name>` is refs/tags/<name>
			continue
		}
		spec = strings.TrimPrefix(spec, "+")
		if strings.Contains(spec, "*") || strings.HasPrefix(spec, "^") {
			return false
		}
		src, dst, explicit := strings.Cut(spec, ":")
		if !explicit {
			if mapped || src == "HEAD" || src == "@" {
				return false
			}
			dst = src
		}
		if namesDefault(r, dst) {
			return false
		}
	}
	return true
}

// namesDefault reports whether a destination ref of a push can be the default
// branch: refs/heads/<default>, heads/<default> or the bare name. HEAD names
// the remote's default too.
func namesDefault(r objects.Repo, ref string) bool {
	switch {
	case ref == "HEAD" || ref == "@":
		return true
	case strings.HasPrefix(ref, "refs/heads/"):
		ref = strings.TrimPrefix(ref, "refs/heads/")
	case strings.HasPrefix(ref, "refs/"):
		return false // a tag, a note, or any ref outside the branches
	case strings.HasPrefix(ref, "heads/"):
		ref = strings.TrimPrefix(ref, "heads/")
	}
	return ref == r.Default
}

// remoteHasPushRefspecs reports whether remote.<name>.push is set for the
// remote, read from the config without a process. A config that cannot be
// read counts as set: the wall stands when it cannot tell.
func remoteHasPushRefspecs(r objects.Repo, remote string) bool {
	_, found, err := objects.ConfigValue(r.GitDir, `remote "`+remote+`"`, "push")
	return err != nil || found
}
