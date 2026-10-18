// Copied from aphrollo-tools internal/gitenv/sealed.go at 12b3724e.
// Incident #1043: tests wrote into the real repository and the global git config.

package testiso

import (
	"os"
	"path/filepath"
	"strings"
)

// CeilingList is the GIT_CEILING_DIRECTORIES value naming each of dirs, in the
// spelling given and, where a symlink makes it differ, the resolved one: git
// compares the ceiling against the path it walks, which may be either. An
// empty dir names nothing.
func CeilingList(dirs ...string) string {
	var list []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		list = append(list, d)
		if resolved, err := filepath.EvalSymlinks(d); err == nil && resolved != d {
			list = append(list, resolved)
		}
	}
	return strings.Join(list, string(os.PathListSeparator))
}
