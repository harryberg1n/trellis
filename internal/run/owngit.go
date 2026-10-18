package run

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrOwnGitState is what Light reports for a git child that would write git
// state trellis does not own. Incident: W42, a test-map worktree mutation
// (fanpyp report 2026-10-17): a child of a tool wrote a ref and an index of
// the lane it was reading. trellis writes refs under refs/trellis/ only, and
// never a lane's index.
var ErrOwnGitState = errors.New("trellis: refusing to write git state outside refs/trellis/")

// trellisRefs is the one ref namespace a trellis child may write.
const trellisRefs = "refs/trellis/"

// readVerbs are the git subcommands trellis's own children run that only read
// (hash-object and mktree add objects, never a ref or an index).
var readVerbs = map[string]bool{
	"status": true, "ls-tree": true, "mktree": true, "commit-tree": true, "hash-object": true, "cat-file": true,
	"rev-parse": true, "rev-list": true, "log": true, "diff": true, "show": true, "for-each-ref": true,
	"merge-base": true,
}

// branchWrites are the git branch options that create, move, copy, delete or
// configure a branch.
var branchWrites = []string{
	"-d", "-D", "-m", "-M", "-c", "-C", "-f", "--force", "--delete", "--move", "--copy",
	"-u", "--set-upstream-to", "--unset-upstream", "--edit-description", "--track", "--no-track", "-t",
}

// ownGitState is nil, or an error wrapping ErrOwnGitState when spec is a git
// child that is not on the allowlist of what trellis's own children need.
//
// Allowed: the read verbs in readVerbs; config --get*; worktree list; branch
// --list and --show-current; symbolic-ref that only reads; update-ref under
// refs/trellis/ only (stdin scripts included); git with no subcommand
// (--version). Everything else (branch -f, tag, reset, checkout, add,
// read-tree, worktree add, fetch, commit, update-index, write-tree, ...) is
// refused before it starts.
//
// "The index of a lane" is the index git opens for the checkout at Dir, or at
// the -C directory: <git dir>/index, the primary checkout's and a linked
// worktree's alike. A Spec has no environment and Light removes GIT_INDEX_FILE
// from a git child's, so no trellis child can use a private index: any verb
// that writes an index writes a lane's, and none is on the list.
func ownGitState(spec Spec) error {
	if !isGit(spec.Name) {
		return nil
	}
	g := ParseGitArgs(spec.Args)
	verb, args := g.Verb, g.Rest
	var bad string
	switch {
	case verb == "" || readVerbs[verb]:
	case verb == "update-ref":
		bad = firstOutsideRef(updateRefTargets(args, spec.Stdin))
	case verb == "symbolic-ref":
		bad = firstOutsideRef(symbolicRefTarget(args))
	case verb == "config":
		if !hasPrefixArg(args, "--get") {
			bad = "(not --get)"
		}
	case verb == "worktree":
		if len(args) == 0 || args[0] != "list" {
			bad = "(only list)"
		}
	case verb == "branch":
		if !slices.Contains(args, "--list") && !slices.Contains(args, "--show-current") || slices.ContainsFunc(args, func(a string) bool { return slices.Contains(branchWrites, a) }) {
			bad = "(only --list and --show-current)"
		}
	default:
		bad = "(not a subcommand trellis children run)"
	}
	if bad == "" {
		return nil
	}
	return fmt.Errorf("%w: git %s %s", ErrOwnGitState, verb, bad)
}

func hasPrefixArg(args []string, prefix string) bool {
	return slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, prefix) })
}

// updateRefTargets is the refs a git update-ref would write: its first operand,
// or every ref its --stdin commands name.
func updateRefTargets(args []string, stdin []byte) []string {
	var operands []string
	fromStdin := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-m":
			i++
		case a == "--stdin":
			fromStdin = true
		case strings.HasPrefix(a, "-"):
		default:
			operands = append(operands, a)
		}
	}
	if fromStdin {
		return stdinRefs(stdin)
	}
	if len(operands) == 0 {
		return nil
	}
	return operands[:1]
}

// stdinRefs is the ref each command of an update-ref --stdin script names. A
// record is a line (or, with -z, a NUL-terminated field); a command word is
// followed by its ref.
func stdinRefs(stdin []byte) []string {
	var refs []string
	for _, rec := range strings.FieldsFunc(string(stdin), func(r rune) bool { return r == '\n' || r == 0 }) {
		f := strings.Fields(rec)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "update", "create", "delete", "verify", "symref-update", "symref-create", "symref-delete", "symref-verify":
			refs = append(refs, f[1])
		}
	}
	return refs
}

// symbolicRefTarget is the ref a git symbolic-ref would write: its name
// operand, when it sets (two operands) or deletes (-d) rather than reads.
func symbolicRefTarget(args []string) []string {
	var operands []string
	deleting := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-m":
			i++
		case a == "-d" || a == "--delete":
			deleting = true
		case strings.HasPrefix(a, "-"):
		default:
			operands = append(operands, a)
		}
	}
	if len(operands) == 0 || len(operands) < 2 && !deleting {
		return nil
	}
	return operands[:1]
}

// firstOutsideRef is the first of refs that is not a plain ref under
// refs/trellis/, or "".
func firstOutsideRef(refs []string) string {
	for _, r := range refs {
		rest, ok := strings.CutPrefix(r, trellisRefs)
		if !ok || rest == "" || strings.Contains(r, "..") || strings.Contains(r, "//") || strings.Contains(r, "/./") {
			return r
		}
	}
	return ""
}
