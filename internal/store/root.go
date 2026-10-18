// Package store is trellis's state on disk: the root, the event log, the
// checkpoint and the lock. Time comes from clock.Clock only.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Root resolves the state root for the repo at repoPath: TRELLIS_STATE if
// set, else the platform default. It refuses a root inside the repo, so state
// is never written into the working tree. An empty repoPath means no
// repository is involved, so there is nothing to be inside of: the home
// directory of a user is not a repo and the default root lives under it.
func Root(repoPath string) (string, error) {
	root := os.Getenv("TRELLIS_STATE")
	if root == "" {
		var err error
		if root, err = defaultRoot(os.Getenv); err != nil {
			return "", err
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if repoPath == "" {
		return absRoot, nil
	}
	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return "", err
	}
	if _, inside := Within(Canonical(absRepo), Canonical(absRoot)); inside {
		return "", fmt.Errorf("state root %s is inside the repo %s", absRoot, absRepo)
	}
	return absRoot, nil
}

// RepoID names a repo: the first 16 hex digits of sha256 of its remote URL,
// or of its cleaned absolute path (case-folded on Windows) when it has no remote.
func RepoID(remoteURL, absPath string) string {
	key := remoteURL
	if key == "" {
		key = normPath(absPath)
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}

// RepoDir is the per-repo directory under root.
func RepoDir(root, repoID string) string {
	return filepath.Join(root, "repos", repoID)
}

// Within is p relative to dir ("." for dir itself) and true when p is dir or
// below it. Both paths are compared as given: no symlink is resolved and no
// case is folded. It is the one path-containment test of the module's
// production code.
func Within(dir, p string) (rel string, ok bool) {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}
