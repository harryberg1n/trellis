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
func RecordRepo(root, repo string, opts ...RecordOption) error {
	canon, err := canonicalRepo(repo)
	if err != nil {
		return err
	}
	return addLine(root, reposFile, canon, opts...)
}

// addLine adds line to the record file once, holding the records lock across
// the read and the write.
func addLine(root, name, line string, opts ...RecordOption) error {
	return lockRecords(root, func() error {
		lines, err := readLines(root, name)
		if err != nil {
			return err
		}
		if slices.Contains(lines, line) {
			return nil
		}
		return writeLines(root, name, append(lines, line))
	}, opts...)
}

// ForgetRepo removes repo from the record; one not there is not an error.
func ForgetRepo(root, repo string) error {
	canon, err := canonicalRepo(repo)
	if err != nil {
		return err
	}
	return lockRecords(root, func() error {
		repos, err := Repos(root)
		if err != nil {
			return err
		}
		kept := slices.DeleteFunc(slices.Clone(repos), func(r string) bool { return r == canon })
		if len(kept) == len(repos) {
			return nil
		}
		return writeLines(root, reposFile, kept)
	})
}

// CreatedPins is the versions `trellis update` pinned itself.
func CreatedPins(root string) ([]string, error) { return readLines(root, pinsFile) }

// RecordCreatedPin notes that update wrote the pinned binary of version. It is
// called after the pin is written, so a crash between the two leaves a pin no
// one will sweep.
func RecordCreatedPin(root, version string, opts ...RecordOption) error {
	return addLine(root, pinsFile, version, opts...)
}

// ReferencedVersions is the versions the trellis hooks in the user-level
// settings of configDir point at, each once, in the order they appear. No
// settings file is none.
func ReferencedVersions(root, configDir string) ([]string, error) {
	file := SettingsFile(configDir)
	doc, err := readSettings(file)
	if err != nil {
		return nil, err
	}
	found, err := walkEntries(doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
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

// LinkedWorktrees is the linked worktrees of the repository around repo, read
// from the gitdir file each one has under <common dir>/worktrees, the same
// list `git worktree list` prints; the primary checkout is not among them.
func LinkedWorktrees(repo string) ([]string, error) {
	r, err := objects.Open(repo)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(r.Common, "worktrees"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		admin := filepath.Join(r.Common, "worktrees", e.Name())
		b, err := os.ReadFile(filepath.Join(admin, "gitdir"))
		if err != nil {
			continue // a half-made or half-removed worktree has nothing to read
		}
		dotGit := filepath.FromSlash(strings.TrimSpace(string(b)))
		if !filepath.IsAbs(dotGit) {
			// git 2.48 worktree.useRelativePaths: relative to the admin directory
			dotGit = filepath.Join(admin, dotGit)
		}
		if _, err := os.Stat(dotGit); err != nil {
			return nil, fmt.Errorf("linked worktree %s is not found (deleted or unmounted?): git worktree prune forgets it, and trellis update continues once it is pruned", filepath.Dir(dotGit))
		}
		out = append(out, store.Canonical(filepath.Dir(dotGit)))
	}
	return out, nil
}
