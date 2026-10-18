package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrBadRowID is what Allow and TakeAllow report for an id that is not a row
// name: lower-case letters, digits and hyphens only, so an id can never name a
// path outside the allow directory.
var ErrBadRowID = errors.New("store: not a row id")

var rowID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func (s *Store) allowPath(id string) (string, error) {
	if !rowID.MatchString(id) {
		return "", fmt.Errorf("%w: %q", ErrBadRowID, id)
	}
	return filepath.Join(s.dir, "allow", id), nil
}

// AllowTTL is how long a token waives a deny. An older token is ignored and
// removed: an allow is for the command the person is about to run, not for one
// that comes days later.
const AllowTTL = 10 * time.Minute

// stamp is the time layout of a token's content.
const stamp = time.RFC3339

// Allow leaves a one-shot token for the row: the next deny of that row is
// waived, once, within AllowTTL. Allowing a row twice before it denies is
// still one waiver. Tokens live in this store's directory, so they are per
// repo id and row. A repo with no origin remote is keyed by its worktree path:
// a token minted in the primary checkout is not seen from a lane.
func (s *Store) Allow(id string) error {
	p, err := s.allowPath(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(s.clock.Now().UTC().Format(stamp)+"\n"), 0o644)
}

// valid reports whether the row has a token younger than AllowTTL. An expired
// or unreadable token is removed.
func (s *Store) valid(id string) (bool, error) {
	p, err := s.allowPath(id)
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	at, perr := time.Parse(stamp, strings.TrimSpace(string(raw)))
	if perr != nil || s.clock.Now().Sub(at) > AllowTTL {
		if rerr := os.Remove(p); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return false, rerr
		}
		return false, nil
	}
	return true, nil
}

// TakeAllow consumes the row's token and reports whether there was a valid
// one. It is a single remove, so of two hooks racing for one token exactly one
// gets it.
func (s *Store) TakeAllow(id string) (bool, error) {
	return s.TakeAllows(id)
}

// TakeAllows consumes the tokens of all the rows, or none: false when any of
// them has no valid token, and then the others are left alone.
func (s *Store) TakeAllows(ids ...string) (bool, error) {
	for _, id := range ids {
		ok, err := s.valid(id)
		if err != nil || !ok {
			return false, err
		}
	}
	for _, id := range ids {
		p, _ := s.allowPath(id)
		switch err := os.Remove(p); {
		case err == nil:
		case errors.Is(err, fs.ErrNotExist):
			return false, nil // a racing hook took it
		default:
			return false, err
		}
	}
	return true, nil
}
