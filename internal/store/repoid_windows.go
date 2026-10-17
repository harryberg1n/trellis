package store

import (
	"path/filepath"
	"strings"
)

// normPath cleans p and folds its case: Windows paths are case-insensitive.
func normPath(p string) string { return strings.ToLower(filepath.Clean(p)) }
