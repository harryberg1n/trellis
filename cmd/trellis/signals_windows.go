//go:build windows

package main

import "os"

// interruptSignals are the signals that cancel a running verb: on Windows
// Ctrl-C and Ctrl-Break both arrive as os.Interrupt, and there is no SIGTERM.
func interruptSignals() []os.Signal { return []os.Signal{os.Interrupt} }
