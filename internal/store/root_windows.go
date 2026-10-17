package store

import (
	"errors"
	"path/filepath"
)

func defaultRoot(env func(string) string) (string, error) {
	base := env("LOCALAPPDATA")
	if base == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	return filepath.Join(base, "trellis"), nil
}
