//go:build !windows

package store

import "path/filepath"

func normPath(p string) string { return filepath.Clean(p) }
