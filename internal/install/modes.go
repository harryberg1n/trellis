package install

import "io/fs"

// What install creates under the state root is the owner's alone: Windows
// ignores the permission bits, elsewhere another user must not read the record
// of repositories or replace a pinned binary.
const (
	dirMode  fs.FileMode = 0o700
	fileMode fs.FileMode = 0o600
	exeMode  fs.FileMode = 0o700
)

// userDirMode is the mode of the config directory install makes when the user
// has none yet: the user's own, as the harness would make it.
const userDirMode fs.FileMode = 0o755
