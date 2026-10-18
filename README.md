# trellis

trellis records what an AI coding agent does in a repository and puts walls and policies in front of the tool calls that have caused incidents. This is milestone M0: the recorder and the walls.

Install (M0): download the binary for your platform from the latest GitHub release, check it against `sha256sums.txt`, and put it on your `PATH`. Then run `trellis version`.

Tests: `go test -race ./...` is the CI run. `go test -short ./...` skips the hook latency tests (`internal/hook`, `-run Latency`), which are timing measurements and are meant to run without `-race`.

Hooks (M0): `trellis install [--repo R] [--dry]` pins this binary under the state root and adds trellis hook entries to `<repo>/.claude/settings.local.json`; `trellis doctor` checks them; `trellis eject` removes exactly what install added. A build that reports version `dev` is refused unless `--allow-dev` is passed.

Versions: the `VERSION` file at the repository root is the source of truth for the plugin version (`.claude-plugin/plugin.json` must equal it, a test enforces that). A release tag must be `v` plus the content of `VERSION`; the release workflow fails otherwise.
