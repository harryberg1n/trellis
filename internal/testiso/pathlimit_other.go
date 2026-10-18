//go:build !windows

package testiso

// gitPathLimit is the longest worktree path a fixture hands git, 0 where git
// has no such limit: the Windows build names the one git for Windows has.
func gitPathLimit() int { return 0 }
