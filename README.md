# trellis

trellis records what an AI coding agent does in a repository and puts walls and policies in front of the tool calls that have caused incidents. This is milestone M0: the recorder and the walls.

Install: download the binary for your platform from the latest GitHub release, check it against `sha256sums.txt`, and put it on your `PATH`. Then run `trellis version`.

## Verbs

`trellis help` prints the same list.

- `trellis version` (also `--version`) prints the version and commit.
- `trellis hook <event>` records one agent hook payload read from stdin. The hooks that `install` adds run it; it is not typed by hand.
- `trellis report [--issue]` prints speed, cost and labels. `--issue` files the aggregate on `harryberg1n/trellis`, which needs an authenticated `gh`.
- `trellis allow <row-id>` waives the next deny of a wall once, for ten minutes, in this repository. Rows with no override are refused.
- `trellis install [--repo R] [--dry] [--allow-dev]` pins this binary under the state root and adds the six hook entries to `<repo>/.claude/settings.local.json`. `--dry` prints the actions without doing them; `--allow-dev` pins a build whose version is `dev`, which is refused otherwise.
- `trellis doctor [--repo R]` checks the installed hooks against this binary and the state root, and names each event that is missing.
- `trellis eject [--repo R]` removes exactly what `install` added.
- `trellis update [--to VERSION] [--dry] [--allow-dev] [--forget-missing]` fetches the newest release (or `--to`), verifies its sha256, pins it, re-installs every recorded repository, replaces the binary on `PATH` and sweeps old pins. When already at the version it still re-installs a recorded repository whose hooks point at another one. A recorded path that is not a repository root is kept in the record and reported; `--forget-missing` drops it. `update` needs an authenticated `gh`.
- `trellis tree new <name> [--path P]` opens a tree: a git worktree on a new branch of that name, cut from `origin`'s default branch, in a sibling directory. A name cannot be only digits.
- `trellis tree sync` fetches `origin` and rebases this tree onto its default branch.
- `trellis tree merge [--check CMD] [--rebase]` runs the optional check, pushes, opens or finds the pull request, waits for its checks, squash-merges it (`--rebase` keeps the commits) and removes the tree. It needs an authenticated `gh`. A repository with no remote lands the tree locally as one squash commit (`--rebase`: a `--no-ff` merge) and needs no `gh`. A repository with a remote needs `origin` and `origin/HEAD` (`git remote set-head origin -a`); trellis does not guess a base.
- `trellis tree rm [<name>] [--force]` removes a tree and its branch. It refuses uncommitted work, and commits that are neither merged into the default branch (a squash- or rebase-merged pull request counts, found with `gh`; in a repository with no remote, a `Merge tree <name>` commit on the default branch) nor pushed to the branch's upstream, unless `--force`.

## Exit codes

0 done. 1 an error. 2 a failure you can fix and run again (red or cancelled checks, a conflict, a branch that is behind or blocked, a head that moved), or a usage mistake. 130 `tree`, `update` or `report` was interrupted by Ctrl-C or a terminate signal; the running child and its process tree are ended first, and a local squash in flight is undone.

## Tests

`go test -race ./...` is the CI run. `go test -short ./...` skips the hook latency tests (`internal/hook`, `-run Latency`), which are timing measurements and are meant to run without `-race`.

## Versions and releases

The `VERSION` file at the repository root is the source of truth for the version (`.claude-plugin/plugin.json` must equal it, a test enforces that). A push to `main` that changes `VERSION` is a release: the workflow builds `linux/amd64`, `linux/arm64` and `windows/amd64`, smoke-runs the Linux binary and requires its `version` to name the tag, then creates the GitHub Release `v<VERSION>` with `sha256sums.txt`. So a VERSION bump is a release. A hand-pushed `v*` tag also builds, and must equal `v` plus the content of `VERSION`; the workflow fails otherwise.
