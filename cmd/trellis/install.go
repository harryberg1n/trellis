package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/harryberg1n/trellis/internal/buildinfo"
	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/install"
	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/store"
)

// installEnv is what install, eject and doctor take from the process: the
// working directory and the running binary.
type installEnv struct {
	Cwd   string
	Exe   string
	Clock clock.Clock // names the backup of the settings file; nil is the wall clock
}

func realInstallEnv() (installEnv, error) {
	wd, err := os.Getwd()
	if err != nil {
		return installEnv{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return installEnv{}, err
	}
	return installEnv{Cwd: wd, Exe: exe, Clock: clock.Real()}, nil
}

// installVerb runs `trellis install|eject|doctor` in the real process;
// args[0] is the verb.
func installVerb(args []string, stdout, stderr io.Writer) int {
	env, err := realInstallEnv()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis %s: %v\n", args[0], err)
		return 1
	}
	return runInstallVerb(args, stdout, stderr, env)
}

// runInstallVerb parses the flags the three verbs share (--repo) and runs one.
func runInstallVerb(args []string, stdout, stderr io.Writer, env installEnv) int {
	verb := args[0]
	fs := newFlagSet(verb, installUsage(verb), stderr)
	configDir := fs.String("config-dir", "", "the Claude config directory (default: $CLAUDE_CONFIG_DIR, else ~/.claude)")
	repo := fs.String("repo", "", "the repository (default: the one holding the working directory, if any)")
	dry := fs.Bool("dry", false, "install only: print the actions without performing them")
	allowDev := fs.Bool("allow-dev", false, "install only: pin a build whose version is \"dev\"")
	if err := fs.Parse(args[1:]); err != nil {
		return parseExit(err)
	}
	if (*dry || *allowDev) && verb != "install" {
		_, _ = fmt.Fprintf(stderr, "trellis %s: --dry and --allow-dev are install flags\n", verb)
		return 2
	}
	opts, err := installOptions(*repo, *configDir, env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis %s: %v\n", verb, err)
		return 1
	}
	switch verb {
	case "install":
		opts.Dry, opts.AllowDev = *dry, *allowDev
		return installCmd(opts, stdout, stderr)
	case "eject":
		return ejectCmd(opts, stdout, stderr)
	}
	return doctorCmd(opts, stdout)
}

// installOptions resolves the config directory, the repository and the state
// root for it. Without --repo, a working directory outside any repository is
// not an error: the user-level settings are checked or changed on their own.
func installOptions(repoFlag, configFlag string, env installEnv) (install.Options, error) {
	configDir, err := install.ConfigDir(configFlag)
	if err != nil {
		return install.Options{}, err
	}
	dir := repoFlag
	if dir == "" {
		dir = env.Cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return install.Options{}, err
	}
	opts := install.Options{ConfigDir: configDir, Exe: env.Exe, Version: buildinfo.Version, Clock: env.Clock}
	// Repo is the checkout the verb runs in: the marker is its own. The state root
	// and the record are the primary's, which install.Options resolves itself.
	primary := "" // the primary checkout: it keys the state root and the record
	repo, err := objects.Open(abs)
	switch {
	case errors.Is(err, objects.ErrNotARepo) && repoFlag == "":
		// no repository: the user-level settings only
	case err != nil:
		return install.Options{}, err
	default:
		opts.Repo, primary = repo.Root, repo.PrimaryRoot()
	}
	if opts.Root, err = store.Root(primary); err != nil {
		return install.Options{}, err
	}
	return opts, nil
}

// installCmd prints one line per action; with Dry the lines start "would ".
func installCmd(opts install.Options, stdout, stderr io.Writer) int {
	actions, err := install.Install(opts)
	return printResult("install", actions, err, stdout, stderr)
}

// printResult prints the action lines, or the error, and returns the exit code.
func printResult(verb string, actions []string, err error, stdout, stderr io.Writer) int {
	for _, a := range actions {
		_, _ = fmt.Fprintln(stdout, a)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis %s: %v\n", verb, err)
		return 1
	}
	return 0
}
