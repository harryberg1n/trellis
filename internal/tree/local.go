package tree

import (
	"fmt"
	"strings"

	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/walls"
)

// mergeLocal lands the current tree in a repository with no remote: a --no-ff
// merge into the default branch, in the primary checkout. --no-ff keeps the
// tree visible in history, which is what the report reads a merge from.
func mergeLocal(env Env, w where, check string) error {
	repo := w.Repo
	name := repo.Branch
	dirty, err := walls.Dirty(env.git(), repo.Root)
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	if dirty != "" {
		return fmt.Errorf("tree merge: %s has %s; commit or discard it first", name, dirty)
	}
	if check != "" {
		if err := runCheck(env, repo.Root, name, check); err != nil {
			return err
		}
	}
	primary, err := objects.Open(w.Primary)
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	if primary.Branch != repo.Default {
		return fmt.Errorf("tree merge: the primary checkout is on %s, not %s", primary.Branch, repo.Default)
	}
	dirty, err = walls.Dirty(env.git(), w.Primary)
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	if dirty != "" {
		return fmt.Errorf("tree merge: the primary checkout has %s; commit or discard it first", dirty)
	}

	args := []string{"merge", "--no-ff", "--no-edit", "-m", "Merge tree " + name}
	if body := mergeBody(env, w.Primary, repo.Default, name); body != "" {
		args = append(args, "-m", body)
	}
	if _, err := env.runGit(w.Primary, append(args, name)...); err != nil {
		files := lines(mustGit(env, w.Primary, "diff", "--name-only", "--diff-filter=U"))
		if len(files) == 0 {
			return fmt.Errorf("tree merge: %w", err)
		}
		_, _ = env.runGit(w.Primary, "merge", "--abort")
		return &Fixable{Msg: fmt.Sprintf("tree merge: conflicts with %s in: %s\nrun trellis tree sync in the tree, resolve, merge again",
			repo.Default, strings.Join(files, ", "))}
	}
	sha, err := env.runGit(w.Primary, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	env.record(localMergedEvent(name, sha))
	env.say("merged %s into %s at %s", name, repo.Default, short(sha))
	return removeTree(env, w, repo.Root, name, false)
}

// mergeBody is the first-parent subject lines of the commits name adds to
// base, one "- subject" each, oldest first.
func mergeBody(env Env, dir, base, name string) string {
	subjects := lines(mustGit(env, dir, "log", "--first-parent", "--reverse", "--format=%s", base+".."+name))
	for i, s := range subjects {
		subjects[i] = "- " + s
	}
	return strings.Join(subjects, "\n")
}
