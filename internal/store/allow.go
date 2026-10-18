package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// Allow leaves a one-shot token for the row: the next deny of that row is
// waived, once. Allowing a row twice before it denies is still one waiver.
func (s *Store) Allow(id string) error {
	p, err := s.allowPath(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(s.clock.Now().UTC().Format("2006-01-02T15:04:05Z")+"\n"), 0o644)
}

// TakeAllow consumes the row's token and reports whether there was one. It is
// a single remove, so of two hooks racing for one token exactly one gets it.
func (s *Store) TakeAllow(id string) (bool, error) {
	p, err := s.allowPath(id)
	if err != nil {
		return false, err
	}
	switch err := os.Remove(p); {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}
