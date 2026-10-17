# trellis: developer context

Go module `github.com/harryberg1n/trellis`, Go 1.26.9. Dependencies: the standard library, `golang.org/x/sys` for Windows process calls, `pgregory.net/rapid` for property tests. The spec and plan live in `docs/sdd/` and are local only (git-ignored).

## Rules

- One builder at a time, in lane order. Each lane is one PR, rebased on `origin/main`, never merged by the builder.
- TDD: a failing test first for new code. A test's expectation is a literal, never a value computed by the code under test.
- No test sleeps. Time comes from `clock.Clock`. A test that fails under load is a broken test.
- Platform code goes in `_windows.go` / `_other.go` pairs, never an inline `runtime.GOOS`.
- Commits and PRs carry no attribution trailer or footer, and never a tool or model name.
- A file copied from aphrollo-tools starts with a comment naming its source path at aphrollo-tools `12b3724e` and the incident it encodes. It is copied, never imported. aphrollo-tools is read, never changed.
- aphrollo is off in this repo. Run `go test -race ./...`, `go vet ./...` and `gofmt -l .` (it must print nothing) yourself.

## Test isolation

Every package with tests has `func TestMain(m *testing.M) { testiso.Main(m) }`. It moves HOME, the temp dirs and the git config under a throwaway root, so a fixture's bare `git init` or `git config --global` cannot reach the real repository or the real `~/.gitconfig`. `testiso.Repo(t)` gives a fresh committed repo; `testiso.Verify(root)` refuses a root outside the sandbox. A test enforces that every package with tests calls it.

## Layering

Dependencies point down only. `internal/kernel` and `internal/measure` import the standard library and kernel types only: no `os`, no `os/exec`, no clock call (`time.Now`, `Since`, `Until`, `Sleep`, `After`, `Tick`, `NewTimer`, `NewTicker`). The `time.Duration` type is allowed. Effects come back as events. `internal/layering` enforces this over `go/parser` ASTs; CI runs it as the `layering` job.

## Release

A `v*` tag builds `linux/amd64`, `linux/arm64` and `windows/amd64` with `-trimpath` and the `buildinfo` ldflags, and attaches them with `sha256sums.txt`.
