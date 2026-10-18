package run

import (
	"errors"
	"fmt"
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

// ownGitState is nil, or an error wrapping ErrOwnGitState when spec is a git
// child that would write a ref outside refs/trellis/ or a lane's index.
//
// The refused argv are git update-ref and git symbolic-ref (set or delete)
// naming a ref outside refs/trellis/, and git update-index and git write-tree.
// "The index of a lane" is the index git opens for the checkout at Dir, or at
// the -C directory: <git dir>/index, the primary checkout's and a linked
// worktree's alike. A Spec has no environment and Light removes GIT_INDEX_FILE
// from a git child's, so no trellis child can use a private index: every
// update-index and write-tree it starts reads or writes a lane's. An argv it
// builds a tree with (mktree, commit-tree, hash-object) touches no index.
func ownGitState(spec Spec) error {
	if !isGit(spec.Name) {
		return nil
	}
	verb, args := gitVerb(spec.Args)
	var bad string
	switch verb {
	case "update-ref":
		bad = firstOutsideRef(updateRefTargets(args, spec.Stdin))
	case "symbolic-ref":
		bad = firstOutsideRef(symbolicRefTarget(args))
	case "update-index", "write-tree":
		bad = "the index of the checkout"
	}
	if bad == "" {
		return nil
	}
	return fmt.Errorf("%w: git %s %s", ErrOwnGitState, verb, bad)
}

// gitVerb is the subcommand of a git argv and the arguments after it, with
// git's own leading options skipped.
func gitVerb(args []string) (verb string, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case !strings.HasPrefix(a, "-"):
			return a, args[i+1:]
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace" ||
			a == "--super-prefix" || a == "--config-env":
			i++ // the option's value is the next argument
		}
	}
	return "", nil
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
