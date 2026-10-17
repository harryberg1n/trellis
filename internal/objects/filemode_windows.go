//go:build windows

package objects

// filemodeDefault is git's core.filemode where the repo config does not say:
// off on Windows, where files carry no exec bit.
const filemodeDefault = false
