package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/store"
)

// reposFile is the record of every repository install has put hooks into, one
// canonical root per line, under the state root. `trellis update` reads it.
const reposFile = "repos.txt"

// pinsFile is the record of the pinned versions `trellis update` created, one
// tag per line, under the state root. update sweeps only those: a pin made by
// an install, in a repo the record may not know, is never removed.
const pinsFile = "pins.txt"

func canonicalRepo(repo string) (string, error) {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	return store.Canonical(abs), nil
}

// readLines is the non-empty lines of the file at root/name; none when absent.
func readLines(root, name string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(root, name))
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

func writeLines(root, name string, lines []string) error {
	if err := os.MkdirAll(root, dirMode); err != nil {
		return err
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	return store.WriteAtomic(root, name, []byte(b.String()), store.WithMode(fileMode))
}

// Repos is the recorded repository roots, in the order they were recorded.
// No record is no repositories.
func Repos(root string) ([]string, error) { return readLines(root, reposFile) }

// RecordRepo adds repo to the record, once.
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
	return writeLines(root, reposFile, append(repos, canon))
}

// recordInstall records the repository an install just wrote hooks into, and
// returns a note when it recorded nothing. A linked worktree is recorded as its
// primary checkout, but only when the primary holds trellis hooks of its own:
// update re-installs what it records, and must not write hooks into a checkout
// the user never installed in. A tree that `trellis tree rm` later removes
// leaves no record that is not found.
func recordInstall(root, repo string) (note string, err error) {
	canon, err := canonicalRepo(repo)
	if err != nil {
		return "", err
	}
	r, oerr := objects.Open(canon)
	if oerr != nil || r.Primary || filepath.Base(r.Common) != ".git" {
		return "", RecordRepo(root, canon)
	}
	primary := filepath.Dir(r.Common)
	refs, err := ReferencedVersions(root, primary)
	if err != nil {
		return "", err
	}
	if len(refs) == 0 {
		return "not recorded: update re-installs primary checkouts only; run trellis install in " + primary + " to include this repository", nil
	}
	return "", RecordRepo(root, primary)
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
	return writeLines(root, reposFile, kept)
}

// CreatedPins is the versions `trellis update` pinned itself.
func CreatedPins(root string) ([]string, error) { return readLines(root, pinsFile) }

// RecordCreatedPin notes that update wrote the pinned binary of version. It is
// called after the pin is written, so a crash between the two leaves a pin no
// one will sweep.
func RecordCreatedPin(root, version string) error {
	pins, err := CreatedPins(root)
	if err != nil {
		return err
	}
	if slices.Contains(pins, version) {
		return nil
	}
	return writeLines(root, pinsFile, append(pins, version))
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
