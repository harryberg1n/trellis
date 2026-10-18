# trellis

trellis records what an AI coding agent does in a repository and puts walls and policies in front of the tool calls that have caused incidents. This is milestone M0: the recorder and the walls.

Install: download the binary for your platform from the latest GitHub release, check it against `sha256sums.txt`, and put it on your `PATH`. Then run `trellis version`.

## Verbs

`trellis help` prints the same list.

- `trellis version` (also `--version`) prints the version and commit.
- `trellis hook <event>` records one agent hook payload read from stdin. The hooks that `install` adds run it; it is not typed by hand.
- `trellis report [--issue]` prints speed, cost and labels. `--issue` files the aggregate on `harryberg1n/trellis`, which needs an authenticated `gh`. What the report could not read is a `not read: …` line in the body, so a filed issue says it. A mean over fewer than 10 samples prints `n=<k>, no interval`. Transcripts include the session's `subagents/*.jsonl`, counted once per request; lines or files that could not be read are counted (`transcript lines skipped`, `transcript files not read`). In the filed issue a model id with no price is shown as its size (`large model`, `medium model`, `small model`, `other model`).
- `trellis allow <row-id>` waives the next deny of a wall once, for ten minutes, in this repository: the token is repo-wide, shared by the primary and every tree, and whichever is denied first consumes it. Rows with no override are refused.
- `trellis install [--config-dir D] [--repo R] [--dry] [--allow-dev]` pins this binary under the state root, wires the six hook entries once per user in `settings.json` of the Claude config directory (`--config-dir`, else `$CLAUDE_CONFIG_DIR`, else `~/.claude`; an existing file is copied beside itself as `settings.json.pre-trellis-<unix seconds>` before its first change), writes `trellis.toml` at the repository root when it has none, and removes trellis's entries from the repository's `.claude/settings.local.json` and from each linked worktree's. It prints one line per action; `--dry` prints them with `would` and does nothing; `--allow-dev` pins a build whose version is `dev`, which is refused otherwise.
- `trellis doctor [--config-dir D] [--repo R]` checks the user-level hooks against this binary and the state root, names each event that is missing, says `trellis.toml missing: this repository is off` when the repository has none, and names a project-local file that still holds trellis entries (every event would fire twice). A clean box prints `doctor: ok`.
- `trellis eject [--config-dir D] [--repo R]` removes exactly what `install` added, from the user-level file and from the repository's project-local files. It leaves `trellis.toml`, which is the repository's own committed file.
- `trellis update [--config-dir D] [--to VERSION] [--dry] [--allow-dev] [--forget-missing]` fetches the newest release (or `--to`), verifies its sha256, pins it, points the user-level hooks at it (only a file that already holds trellis hooks; `update` never installs), removes trellis's entries from the project-local files of every recorded repository and its trees, replaces the binary on `PATH` and sweeps old pins. When already at the version it still rewires hooks that point at another one. A recorded path that is not a repository root is kept in the record and reported; `--forget-missing` drops it. `update` needs an authenticated `gh`.
- `trellis tree new <name> [--path P]` opens a tree: a git worktree on a new branch of that name, cut from `origin`'s default branch, in a sibling directory. A name cannot be only digits.
- `trellis tree sync` fetches `origin` and rebases this tree onto its default branch.
- `trellis tree merge [--check CMD] [--rebase]` runs the optional check, pushes, opens or finds the pull request, waits for its checks, squash-merges it (`--rebase` keeps the commits) and removes the tree. It needs an authenticated `gh`. A repository with no remote lands the tree locally as one squash commit (`--rebase`: a `--no-ff` merge) and needs no `gh`. A repository with a remote needs `origin` and `origin/HEAD` (`git remote set-head origin -a`); trellis does not guess a base.
- `trellis tree rm [<name>] [--force]` removes a tree and its branch. It refuses uncommitted work, and commits that are neither merged into the default branch (a squash- or rebase-merged pull request counts, found with `gh`; in a repository with no remote, a `Merge tree <name>` commit on the default branch) nor pushed to the branch's upstream, unless `--force`.

A repository is on once `trellis.toml` is committed at its root, found by walking up from the working directory to the first `.git`; a linked worktree of it is on too. Anywhere else, and under `TRELLIS_OFF=1`, the hook exits 0 with no output and writes nothing. Everything a tree writes goes to the store of the primary checkout.

## Walls

A deny is `trellis: <row>: <cause>. Override: <command>.`, one line except the secrets deny, which lists its findings under it; the command is `trellis allow <row>` or `none`. What the walls read, where it is not obvious:

- Command names are matched without case on Windows (`Git reset --hard`, `GIT.EXE commit --no-verify` are `git`); elsewhere `Git` is not `git`.
- `discard-uncommitted` measures `git clean` with `-x` or `-X` over the ignored files too: `clean -fdx discards 1 untracked file, 1 ignored file` (`-X` counts only the ignored ones). `git stash drop` and `git stash clear` on a repository with no stash entry pass; with one they say `stash drop discards a stash entry for good`.
- `undercover` reads the text of `git commit` and `git merge` (`-m`, `-F`), the title, squash subject and body of `gh pr create|edit|merge` (`-t`, `--title`, `--subject`, `-b`, `--body-file`), and the identity a commit is made with: `--author=` or `--author <ident>`, and `-c user.name=` or `-c user.email=` before the verb, for the verbs that write a commit (`commit`, `merge`, `cherry-pick`, `revert`, `am`, `rebase`, `pull`; `git log --author=` is a search and passes). A vendor address (`@anthropic.com`, `noreply@openai.com`, `cursoragent@cursor.com`, `<digits>+copilot@users.noreply.github.com`) is a deny on any line or in an identity: `the author or committer identity of this commit carries a vendor address ("noreply@anthropic.com")`. A name alone is not a tell.
- `secrets` runs gitleaks over the staged changes (`gitleaks git --staged` from 8.19, `protect --staged` before; the version is asked once per binary path and mtime and kept in `tool-versions.json` in the state root) and, for a commit that stages as it goes, over the uncommitted changes. A finding is listed as `file:line  rule  fingerprint <fingerprint>`, followed by the three ways to clear a false positive: `gitleaks:allow` in a comment on that line, the fingerprint in `.gitleaksignore`, or an `[allowlist]` in `.gitleaks.toml`. The row has no override.
- A rebase in progress keeps its branch: the recorder reads `rebase-merge/head-name` and `rebase-apply/head-name` from the worktree's own git directory, so edits during a conflict stay on the tree, not on `detached-<sha>`. A rebase of the default branch in the primary checkout therefore counts as being on it, so `primary-write` denies the edits of its conflict resolution: the primary is merge-only, and `trellis tree merge` refuses a primary stuck mid-rebase.

## Exit codes

0 done. 1 an error. 2 a failure you can fix and run again (red or cancelled checks, a conflict, a branch that is behind or blocked, a head that moved), or a usage mistake. 130 `tree`, `update` or `report` was interrupted by Ctrl-C or a terminate signal; the running child and its process tree are ended first, and a local squash in flight is undone.

## Tests

`go test -race -shuffle=on ./...` is the CI run. Hook latency is not a test: every event the hook writes carries `took_ms`, and `trellis report` prints p50 and p95 per hook against the criteria over the live log. A nightly flake hunt runs `go test -race -shuffle=on -count=5 -json` on Linux and Windows and keeps the JSON stream and a plain log as an artifact; a non-zero exit with an empty stream is named an infrastructure failure.

## Versions and releases

The `VERSION` file at the repository root is the source of truth for the version (`.claude-plugin/plugin.json` must equal it, a test enforces that). A push to `main` that changes `VERSION` is a release: the workflow builds `linux/amd64`, `linux/arm64` and `windows/amd64`, smoke-runs the Linux binary and requires its `version` to name the tag, then creates the GitHub Release `v<VERSION>` bare and uploads the binaries with `--clobber`, `sha256sums.txt` last. A re-run after a partial upload finishes it; a release that has its `sha256sums.txt` is left alone, and a `gh release view` failure other than "not found" stops the run. So a VERSION bump is a release. A hand-pushed `v*` tag also builds, and must equal `v` plus the content of `VERSION`; the workflow fails otherwise.
