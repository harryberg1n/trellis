package main

import (
	"fmt"
	"io"

	"github.com/harryberg1n/trellis/internal/install"
)

// doctorCmd prints one line per problem, or `doctor: ok`, and exits 1 when
// there was a problem.
func doctorCmd(opts install.Options, stdout io.Writer) int {
	problems := install.Doctor(opts)
	if len(problems) == 0 {
		_, _ = fmt.Fprintln(stdout, "doctor: ok")
		return 0
	}
	for _, p := range problems {
		_, _ = fmt.Fprintln(stdout, p)
	}
	return 1
}
