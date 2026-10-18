package store

// Windows ignores POSIX modes; access comes from the profile directory's ACL.
const (
	dirMode  = 0o755
	fileMode = 0o644
)
