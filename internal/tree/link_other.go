//go:build !windows

package tree

import "os"

// makeLink makes link a symbolic link to target.
func makeLink(_ Env, target, link string) error {
	return os.Symlink(target, link)
}
