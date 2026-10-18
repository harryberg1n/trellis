package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/harryberg1n/trellis/internal/install"
)

// ejectCmd removes the entries install added and prints what it removed. When
// nothing was removed it says so, ahead of the note that trellis.toml stays.
func ejectCmd(opts install.Options, stdout, stderr io.Writer) int {
	actions, err := install.Eject(opts)
	if err == nil && !slices.ContainsFunc(actions, func(a string) bool { return strings.HasPrefix(a, "remove ") }) {
		_, _ = fmt.Fprintln(stdout, "eject: nothing to remove")
	}
	return printResult("eject", actions, err, stdout, stderr)
}
