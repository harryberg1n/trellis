package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/harryberg1n/trellis/internal/store"
)

// reposFile is the record of every repository install has put hooks into, one
// canonical root per line, under the state root. `trellis update` reads it.
const reposFile = "repos.txt"

func canonicalRepo(repo string) (string, error) {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	return store.Canonical(abs), nil
}

// Repos is the recorded repository roots, in the order they were recorded.
// No record is no repositories.
func Repos(root string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(root, reposFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for line := range strings.SplitSeq(string(b), "\n") {
		if line = strings.TrimRight(line, "\r"); line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

func writeRepos(root string, repos []string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, r := range repos {
		b.WriteString(r + "\n")
	}
	return store.WriteAtomic(root, reposFile, []byte(b.String()), store.WithMode(0o644))
}

// RecordRepo adds repo to the record; one already there is not added twice.
func RecordRepo(root, repo string) error {
	canon, err := canonicalRepo(repo)
	if err != nil {
		return err
	}
	repos, err := Repos(root)
	if err != nil {
		return err
	}
	if slices.Contains(repos, canon) {
		return nil
	}
	return writeRepos(root, append(repos, canon))
}

// ForgetRepo removes repo from the record; one not there is not an error.
func ForgetRepo(root, repo string) error {
	canon, err := canonicalRepo(repo)
	if err != nil {
		return err
	}
	repos, err := Repos(root)
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(slices.Clone(repos), func(r string) bool { return r == canon })
	if len(kept) == len(repos) {
		return nil
	}
	return writeRepos(root, kept)
}

// ReferencedVersions is the versions the trellis hooks in repo's settings point
// at, each once, in the order they appear. No settings file is none.
func ReferencedVersions(root, repo string) ([]string, error) {
	doc, err := readSettings(repo)
	if err != nil {
		return nil, err
	}
	found, err := walkEntries(doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SettingsPath(repo), err)
	}
	var out []string
	for _, l := range found {
		for _, h := range l.Entry.Hooks {
			if v, ok := Owned(h.Command, root); ok && !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	return out, nil
}
