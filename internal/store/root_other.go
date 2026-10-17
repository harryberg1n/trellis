//go:build !windows

package store

import (
	"errors"
	"path/filepath"
)

func defaultRoot(env func(string) string) (string, error) {
	if x := env("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "trellis"), nil
	}
	home := env("HOME")
	if home == "" {
		return "", errors.New("neither XDG_STATE_HOME nor HOME is set")
	}
	return filepath.Join(home, ".local", "state", "trellis"), nil
}
