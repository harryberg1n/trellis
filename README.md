# trellis

trellis records what an AI coding agent does in a repository and puts walls and policies in front of the tool calls that have caused incidents. This is milestone M0: the recorder and the walls.

Install (M0): download the binary for your platform from the latest GitHub release, check it against `sha256sums.txt`, and put it on your `PATH`. Then run `trellis version`.

Tests: `go test -race ./...` is the CI run. `go test -short ./...` skips the hook latency tests (`internal/hook`, `-run Latency`), which are timing measurements and are meant to run without `-race`.
