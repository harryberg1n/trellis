# trellis: developer context

Go module `github.com/harryberg1n/trellis`, Go 1.27.2. Dependencies: the standard library, `golang.org/x/sys` for Windows process calls, `pgregory.net/rapid` for property tests, `go.uber.org/goleak` for goroutine-leak checks (in `testiso.Main`). The spec and plan live in `docs/sdd/` and are local only (git-ignored).

## Rules

- One builder at a time, in tree order. Each tree is one PR, rebased on `origin/main`, never merged by the builder.
- TDD: a failing test first for new code. A test's expectation is a literal, never a value computed by the code under test.
- No test sleeps. Time comes from `clock.Clock`. A test that fails under load is a broken test.
- Platform code goes in `_windows.go` / `_other.go` pairs, never an inline `runtime.GOOS`.
- Commits and PRs carry no attribution trailer or footer, and never a tool or model name.
- A file copied from aphrollo-tools starts with a comment naming its source path at aphrollo-tools `12b3724e` and the incident it encodes. It is copied, never imported. aphrollo-tools is read, never changed.
- aphrollo is off in this repo. Run `go test -race ./...`, `go vet ./...` and `gofmt -l .` (it must print nothing) yourself.

## Test isolation

Every package with tests has `func TestMain(m *testing.M) { testiso.Main(m) }`. It moves HOME, the temp dirs and the git config under a throwaway root, so a fixture's bare `git init` or `git config --global` cannot reach the real repository or the real `~/.gitconfig`. `testiso.Repo(t)` gives a fresh committed repo; `testiso.Verify(root)` refuses a root outside the sandbox. A test enforces that every package with tests calls it.

## Layering

Dependencies point down only. `internal/kernel` and `internal/measure` import the standard library and kernel types only: no `os`, no `os/exec`, no clock call (`time.Now`, `Since`, `Until`, `Sleep`, `After`, `Tick`, `NewTimer`, `NewTicker`). The `time.Duration` type is allowed. Effects come back as events. `internal/layering` enforces this over `go/parser` ASTs; its tests run with the suite on every CI job.

## Release

The push to main that changes `VERSION` makes the release: it builds `linux/amd64`, `linux/arm64` and `windows/amd64` with `-trimpath` and the `buildinfo` ldflags, smoke-runs the Linux binary (its `version` must name the tag), and attaches them with `sha256sums.txt` to the GitHub Release `v<VERSION>`. A `v*` tag pushed by hand also builds, and must equal `v` plus the content of `VERSION`.

## Style

Code follows the Uber Go style guide (https://github.com/uber-go/guide/blob/master/style.md). The mechanical part is `.golangci.yml`, run by CI on Linux and locally with `golangci-lint run ./...` (v2.15.0); `golangci-lint run --fix ./...` applies gofumpt and goimports. The rules that bite most: wrap an error once with `%w`, no package-level mutable state, `os.Exit` only in `main`, `var _ Interface = (*T)(nil)` for compliance, functional options instead of new positional parameters, no `init()`, no goroutine without a known exit, field names in struct literals, imports grouped std, third-party, local. `depguard` keeps `os` and `os/exec` out of `internal/kernel` and `internal/measure`, beside the layering law.
