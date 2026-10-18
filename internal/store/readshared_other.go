// Source: aphrollo-tools 12b3724e internal/tdd/core/readshared_other.go.
// Incident: CHANGELOG 1.5.x "two Windows flakes in the gate's own state writes".

//go:build !windows

package store

import "os"

// openShared is os.Open off Windows: a rename over a file another process has
// open succeeds there, and the open handle goes on reading the old content.
func openShared(path string) (*os.File, error) { return os.Open(path) }
