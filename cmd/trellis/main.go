// Command trellis is the trellis recorder and wall.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"

	"github.com/harryberg1n/trellis/internal/buildinfo"
	runpkg "github.com/harryberg1n/trellis/internal/run"
)

const usage = `usage: trellis <verb>

verbs:
  version   print the version and commit (also --version)
  hook      record one agent hook payload read from stdin: trellis hook <event>
  report    print speed, cost and labels; --issue files them on harryberg1n/trellis
  allow     waive the next deny of a wall once: trellis allow <row-id>
  install   pin this binary and add its hooks to the repo: trellis install [--repo R] [--dry] [--allow-dev]
  doctor    check the installed hooks against this binary and the state root: trellis doctor [--repo R]
  eject     remove the hooks install added: trellis eject [--repo R]
  tree      worktrees over plain git and gh: trellis tree new|sync|merge|rm
  update    fetch the newest release, verify and pin it, re-install every repo: trellis update [--to VERSION] [--dry] [--allow-dev] [--forget-missing]
  help      print this text (also --help, -h)

exit codes: 0 ok; 1 error; 2 a failure you can fix and run again, or a usage mistake; 130 interrupted
(Ctrl-C or a terminate signal during tree, update or report).
`

// exitInterrupted is the exit code of a verb a signal cut short: 128 and SIGINT.
const exitInterrupted = 130

func main() {
	args := os.Args[1:]
	ctx := context.Background()
	if honoursInterrupt(args) {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, interruptSignals()...)
		// Once the first signal has cancelled ctx, give the signals back to the
		// default handling so a second one ends the process at once.
		context.AfterFunc(ctx, stop)
	}
	code := exitCode(ctx, runCtx(ctx, args, os.Stdin, os.Stdout, os.Stderr))
	os.Exit(code)
}

// withReportContext gives every gh and git child of the report the context, so
// a signal ends them.
func withReportContext(env reportEnv, ctx context.Context) reportEnv {
	env.Runner = runpkg.WithContext(env.Runner, ctx)
	return env
}

// honoursInterrupt reports whether the verb in args reads the context a signal
// cancels. Only those verbs may catch the signals: any other verb that did
// would swallow a supervisor's SIGTERM and never die.
func honoursInterrupt(args []string) bool {
	return len(args) > 0 && slices.Contains([]string{"tree", "update", "report"}, args[0])
}

// exitCode is code, or exitInterrupted when ctx was cancelled and the verb did
// not succeed: a signal during a verb that finished anyway is no failure.
func exitCode(ctx context.Context, code int) int {
	if code != 0 && ctx.Err() != nil {
		return exitInterrupted
	}
	return code
}

// run dispatches one verb that reads no stdin and returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	return runIn(args, strings.NewReader(""), stdout, stderr)
}

// runIn dispatches one verb and returns the exit code.
func runIn(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runCtx(context.Background(), args, stdin, stdout, stderr)
}

// runCtx is runIn for a verb that ctx can cancel.
func runCtx(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		_, _ = fmt.Fprintf(stdout, "trellis %s (%s)\n", buildinfo.Version, buildinfo.Commit)
		return 0
	case "hook":
		return hookVerb(args[1:], stdin, stdout, stderr)
	case "report":
		env, err := realReportEnv()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "trellis report: %v\n", err)
			return 1
		}
		env = withReportContext(env, ctx)
		return reportCmd(args[1:], stdout, stderr, env)
	case "allow":
		return allowVerb(args[1:], stdout, stderr)
	case "install", "eject", "doctor":
		return installVerb(args, stdout, stderr)
	case "tree":
		return treeVerb(ctx, args[1:], stdout, stderr)
	case "update":
		return updateVerb(ctx, args[1:], stdout, stderr)
	case "help", "--help", "-h":
		_, _ = io.WriteString(stdout, usage)
		return 0
	}
	_, _ = fmt.Fprintf(stderr, "trellis: unknown verb %q\n\n%s", args[0], usage)
	return 2
}
