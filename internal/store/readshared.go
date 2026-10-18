// Source: aphrollo-tools 12b3724e internal/tdd/core/readshared.go.
// Incident: CHANGELOG 1.5.x "two Windows flakes in the gate's own state writes":
// a plain open on Windows blocks the rename that replaces the file.

package store

import "io"

// readFileShared reads the whole file at path like os.ReadFile, but on Windows
// opens it so that a writer can still replace it by rename while it is held.
// Every file the store publishes with WriteAtomic and another process polls is
// read this way: a plain open there blocks the replace.
func readFileShared(path string) ([]byte, error) {
	f, err := openShared(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
