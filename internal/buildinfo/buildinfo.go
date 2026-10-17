// Package buildinfo holds the version, commit and build time of the binary.
// The release build sets them with -ldflags -X; an unflagged build keeps the
// defaults.
package buildinfo

// Version, Commit and Time are set at link time:
//
//	-X github.com/harryberg1n/trellis/internal/buildinfo.Version=v0.0.1
var (
	Version = "dev"
	Commit  = "unknown"
	Time    = "unknown"
)
