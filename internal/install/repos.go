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

// reposMarker is the first line of a record that an install wrote. A record
// without it may be missing repositories installed before the record existed.
const reposMarker = "# trellis repos v1"

func canonicalRepo(repo string) (string, error) {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	return store.Canonical(abs), nil
}

// readRepos is the recorded roots and whether the marker line is there.
func readRepos(root string) (repos []string, marked bool, err error) {
	b, err := os.ReadFile(filepath.Join(root, reposFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	for i, line := range slices.Collect(strings.SplitSeq(string(b), "\n")) {
		line = strings.TrimRight(line, "\r")
		switch {
		case i == 0 && line == reposMarker:
			marked = true
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			repos = append(repos, line)
		}
	}
	return repos, marked, nil
}

// Repos is the recorded repository roots, in the order they were recorded.
// No record is no repositories.
func Repos(root string) ([]string, error) {
	repos, _, err := readRepos(root)
	return repos, err
}

// ReposComplete reports whether an install wrote the record. Only then does a
// repository's absence from it mean that no hooks of ours are there.
func ReposComplete(root string) (bool, error) {
	_, marked, err := readRepos(root)
	return marked, err
}

func writeRepos(root string, repos []string, marked bool) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	if marked {
		b.WriteString(reposMarker + "\n")
	}
	for _, r := range repos {
		b.WriteString(r + "\n")
	}
	return store.WriteAtomic(root, reposFile, []byte(b.String()), store.WithMode(0o644))
}

// RecordRepo adds repo to the record, once, and marks the record as written by
// an install.
func RecordRepo(root, repo string) error {
	canon, err := canonicalRepo(repo)
	if err != nil {
		return err
	}
	repos, marked, err := readRepos(root)
	if err != nil {
		return err
	}
	if marked && slices.Contains(repos, canon) {
		return nil
	}
	if !slices.Contains(repos, canon) {
		repos = append(repos, canon)
	}
	return writeRepos(root, repos, true)
}

// ForgetRepo removes repo from the record; one not there is not an error. The
// marker stays as it was.
func ForgetRepo(root, repo string) error {
	canon, err := canonicalRepo(repo)
	if err != nil {
		return err
	}
	repos, marked, err := readRepos(root)
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(slices.Clone(repos), func(r string) bool { return r == canon })
	if len(kept) == len(repos) {
		return nil
	}
	return writeRepos(root, kept, marked)
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
