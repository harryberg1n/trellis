//go:build !windows

package main

import (
	"os"
	"syscall"
)

// interruptSignals are the signals that cancel a running verb: Ctrl-C and the
// terminate request a supervisor or a closing terminal sends.
func interruptSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
