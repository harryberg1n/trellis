// Source: aphrollo-tools 12b3724e internal/tdd/core/readshared_windows.go.
// Incident: CHANGELOG 1.5.x "two Windows flakes in the gate's own state writes":
// Windows refuses to replace a file while a handle without delete sharing is
// open on it.

//go:build windows

package store

import (
	"os"

	"golang.org/x/sys/windows"
)

// openShared opens path for reading with delete sharing, which os.Open does
// not: Windows refuses to replace a file while a handle without it is open, so
// a reader polling a file that is rewritten by rename would block the writer
// for as long as it loops. The handle keeps reading the content it opened.
func openShared(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	h, err := windows.CreateFile(name, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// openSharedRW opens an existing path for reading and writing, which fsync
// needs (FlushFileBuffers wants write access), with delete sharing for the
// same reason as openShared: Sync must not block the rename of state.json that
// another hook is doing.
func openSharedRW(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
