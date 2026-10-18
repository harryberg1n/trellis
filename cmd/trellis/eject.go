package main

import (
	"fmt"
	"io"

	"github.com/harryberg1n/trellis/internal/install"
)

// ejectCmd removes the entries install added and prints what it removed.
func ejectCmd(opts install.Options, stdout, stderr io.Writer) int {
	actions, err := install.Eject(opts)
	if err == nil && len(actions) == 0 {
		_, _ = fmt.Fprintln(stdout, "eject: nothing to remove")
	}
	return printResult("eject", actions, err, stdout, stderr)
}
