//go:build windows

package testiso

// gitPathLimit is the longest worktree path a fixture hands git.
//
// Copied from aphrollo-tools internal/tdd/core/gitpathlimit_windows.go at
// 12b3724e. Incident: Git for Windows made a worktree at a path of 215
// characters and refused one of 216 with "fatal: '$GIT_DIR' too big" (measured
// with 2.53.0.windows.2; core.longpaths does not lift it); the limit leaves
// five characters of margin under that.
func gitPathLimit() int { return 210 }
