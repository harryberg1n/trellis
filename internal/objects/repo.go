package objects

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotARepo is what FindRoot returns when no .git lies at or above the path.
var ErrNotARepo = errors.New("objects: not in a git repository")

// FindRoot walks up from dir to the nearest directory holding a usable .git
// (a directory, or the file a linked worktree has, with a HEAD in the git dir
// it names) and returns it. dir need not exist. Like git, it does not climb
// into a directory listed in GIT_CEILING_DIRECTORIES. Nothing is spawned.
func FindRoot(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("objects: %q is not absolute", dir)
	}
	ceilings := ceilingDirs()
	d := filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			if _, gitDir, err := GitDirs(d); err == nil {
				if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); err == nil {
					return d, nil
				}
			}
		}
		up := filepath.Dir(d)
		if up == d || ceilings[up] {
			return "", fmt.Errorf("%w: %s", ErrNotARepo, dir)
		}
		d = up
	}
}

// ceilingDirs is GIT_CEILING_DIRECTORIES as a set of cleaned absolute paths.
func ceilingDirs() map[string]bool {
	out := map[string]bool{}
	for _, c := range filepath.SplitList(os.Getenv("GIT_CEILING_DIRECTORIES")) {
		if filepath.IsAbs(c) {
			out[filepath.Clean(c)] = true
		}
	}
	return out
}

// Branch is the branch HEAD names in gitDir (the repo's own git dir, as
// GitDirs returns it), "feature/x" for refs/heads/feature/x, or "detached"
// "detached-" and the first 12 characters of the commit id when HEAD holds
// one, so detached sessions at different commits get different replay refs.
func Branch(gitDir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(raw))
	ref, ok := strings.CutPrefix(head, "ref:")
	if !ok {
		return "detached-" + head[:min(12, len(head))], nil
	}
	return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/"), nil
}

// DefaultBranch is the repo's default branch, read from files without
// starting git: the branch refs/remotes/origin/HEAD names in the common git
// dir; else init.defaultBranch from the repo's config, if that branch exists;
// else main if refs/heads/main exists, else master if refs/heads/master does;
// else "main".
func DefaultBranch(common string) string {
	if raw, err := os.ReadFile(filepath.Join(common, "refs", "remotes", "origin", "HEAD")); err == nil {
		if ref, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "ref:"); ok {
			if name, ok := strings.CutPrefix(strings.TrimSpace(ref), "refs/remotes/origin/"); ok && name != "" {
				return name
			}
		}
	}
	exists := func(branch string) bool {
		_, found, _ := readRef(common, "refs/heads/"+branch)
		return found
	}
	if name, found, err := ConfigValue(common, "init", "defaultBranch"); err == nil && found && name != "" && exists(name) {
		return name
	}
	if !exists("main") && exists("master") {
		return "master"
	}
	return "main"
}

// IsPrimary reports whether the working tree at repo is the repo's main
// working tree: its git dir is the common dir, as opposed to a linked worktree
// whose git dir is a subdirectory of it.
func IsPrimary(repo string) (bool, error) {
	common, gitDir, err := GitDirs(repo)
	if err != nil {
		return false, err
	}
	return filepath.Clean(common) == filepath.Clean(gitDir), nil
}

// Repo is everything the callers ask of a repository, read from files.
type Repo struct {
	Root    string // the working tree
	Common  string // the git dir that holds objects, refs and config
	GitDir  string // its own git dir (the worktree's, in a linked worktree)
	Branch  string // the branch HEAD names, or "detached-<sha>"
	Primary bool   // the main working tree, not a linked worktree
	Remote  string // the URL of remote origin in the repo config, "" if none
	Default string // the default branch, as DefaultBranch reads it
}

// Open reads the repository around dir without starting a process. It is the
// one place a directory becomes a repository description.
func Open(dir string) (Repo, error) {
	root, err := FindRoot(dir)
	if err != nil {
		return Repo{}, err
	}
	common, gitDir, err := GitDirs(root)
	if err != nil {
		return Repo{}, fmt.Errorf("git dirs of %s: %w", root, err)
	}
	branch, err := Branch(gitDir)
	if err != nil {
		return Repo{}, fmt.Errorf("branch of %s: %w", root, err)
	}
	remote, _, err := ConfigValue(gitDir, `remote "origin"`, "url")
	if err != nil {
		return Repo{}, fmt.Errorf("remote of %s: %w", root, err)
	}
	return Repo{
		Root:    root,
		Common:  common,
		GitDir:  gitDir,
		Branch:  branch,
		Primary: filepath.Clean(common) == filepath.Clean(gitDir),
		Remote:  remote,
		Default: DefaultBranch(common),
	}, nil
}
