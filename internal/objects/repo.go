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

// FindRoot walks up from dir to the nearest directory holding a .git entry (a
// directory, or the file a linked worktree has) and returns it. dir need not
// exist. Nothing is spawned.
func FindRoot(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("objects: %q is not absolute", dir)
	}
	d := filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d, nil
		}
		up := filepath.Dir(d)
		if up == d {
			return "", fmt.Errorf("%w: %s", ErrNotARepo, dir)
		}
		d = up
	}
}

// Branch is the branch HEAD names in gitDir (the repo's own git dir, as
// GitDirs returns it), "feature/x" for refs/heads/feature/x, or "detached"
// when HEAD holds a commit id.
func Branch(gitDir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", err
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "ref:")
	if !ok {
		return "detached", nil
	}
	return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/"), nil
}
