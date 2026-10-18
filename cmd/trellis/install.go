package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/harryberg1n/trellis/internal/buildinfo"
	"github.com/harryberg1n/trellis/internal/install"
	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/store"
)

// installEnv is what install, eject and doctor take from the process: the
// working directory and the running binary.
type installEnv struct {
	Cwd string
	Exe string
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
	return installEnv{Cwd: wd, Exe: exe}, nil
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
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", "", "the repository (default: the one holding the working directory)")
	dry := fs.Bool("dry", false, "install only: print the actions without performing them")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *dry && verb != "install" {
		_, _ = fmt.Fprintf(stderr, "trellis %s: --dry is an install flag\n", verb)
		return 2
	}
	opts, err := installOptions(*repo, env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis %s: %v\n", verb, err)
		return 1
	}
	switch verb {
	case "install":
		opts.Dry = *dry
		return installCmd(opts, stdout, stderr)
	case "eject":
		return ejectCmd(opts, stdout, stderr)
	}
	return doctorCmd(opts, stdout)
}

// installOptions resolves the repository and the state root for it.
func installOptions(repoFlag string, env installEnv) (install.Options, error) {
	dir := repoFlag
	if dir == "" {
		dir = env.Cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return install.Options{}, err
	}
	repo, err := objects.FindRoot(abs)
	if err != nil {
		return install.Options{}, err
	}
	root, err := store.Root(repo)
	if err != nil {
		return install.Options{}, err
	}
	return install.Options{Repo: repo, Root: root, Exe: env.Exe, Version: buildinfo.Version}, nil
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
