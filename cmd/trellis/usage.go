package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// newFlagSet is a flag set whose help reads like the main usage block:
// "usage: trellis <line>", then the flags.
func newFlagSet(name, line string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "usage: trellis %s\n", line)
		fs.PrintDefaults()
	}
	return fs
}

// parseExit is the exit code for a flag parse error: 0 for a request for help,
// 2 for a mistake.
func parseExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}

// installUsage is the usage line of install, eject or doctor.
func installUsage(verb string) string {
	if verb == "install" {
		return "install [--config-dir D] [--repo R] [--dry] [--allow-dev]"
	}
	return verb + " [--config-dir D] [--repo R]"
}
