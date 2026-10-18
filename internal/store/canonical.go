package store

import "path/filepath"

// Canonical is p in one spelling per file: symlinks and 8.3 short names
// resolved, and cleaned. A path that does not exist yet has its longest
// existing ancestor resolved and the rest appended, so the answer for a file
// about to be written agrees with the answer for the directory it lies in.
// Callers compare paths from outside (a payload's cwd, a state root) with a
// repo's paths through Canonical on both sides, then through Within.
func Canonical(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	dir, base := filepath.Split(p)
	if dir == "" || filepath.Clean(dir) == p {
		return p
	}
	return filepath.Join(Canonical(dir), base)
}
