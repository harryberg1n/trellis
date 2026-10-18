//go:build !windows

package store

// The modes of what the store writes under the state root: owner only. The
// log holds paths, commit ids and the text of deny lines.
const (
	dirMode  = 0o700
	fileMode = 0o600
)
