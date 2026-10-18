package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/harryberg1n/trellis/internal/buildinfo"
	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/install"
	"github.com/harryberg1n/trellis/internal/objects"
	runner "github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/update"
)

// updateUsageLine is the usage line of update.
const updateUsageLine = "update [--config-dir D] [--to VERSION] [--dry] [--allow-dev] [--forget-missing]"

// updateVerb runs `trellis update` in the real process.
func updateVerb(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("update", updateUsageLine, stderr)
	configFlag := fs.String("config-dir", "", "the Claude config directory (default: $CLAUDE_CONFIG_DIR, else ~/.claude)")
	to := fs.String("to", "", "switch to this pinned (or released) version instead of the newest release")
	dry := fs.Bool("dry", false, "print each step with \"would \" and change nothing")
	allowDev := fs.Bool("allow-dev", false, "let a build whose version is \"dev\" update")
	forget := fs.Bool("forget-missing", false, "drop a recorded repo that is not found from the record")
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "trellis update: takes no arguments")
		fs.Usage()
		return 2
	}
	root, exe, err := updatePaths()
	var configDir string
	if err == nil {
		configDir, err = install.ConfigDir(*configFlag)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis update: %v\n", err)
		return 1
	}
	env := update.Env{
		Runner:    runner.Exec{Clock: clock.Real()},
		Ctx:       ctx,
		Out:       stdout,
		Root:      root,
		ConfigDir: configDir,
		Exe:       exe,
		Version:   buildinfo.Version,
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
	if err := update.Run(env, update.Options{To: *to, Dry: *dry, AllowDev: *allowDev, ForgetMissing: *forget}); err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis update: %v\n", err)
		return 1
	}
	return 0
}

// updatePaths is the state root and the running binary. The root is checked
// against the repository around the working directory when there is one;
// outside a repository, for example in the home directory, nothing is checked.
func updatePaths() (root, exe string, err error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	repoRoot := ""
	if repo, oerr := objects.Open(wd); oerr == nil {
		repoRoot = repo.Root
	}
	if root, err = store.Root(repoRoot); err != nil {
		return "", "", err
	}
	exe, err = os.Executable()
	return root, exe, err
}
