// Package update is `trellis update`: fetch a release of trellis, verify it,
// pin it under the state root, point the user-level hooks at it, remove the
// project-local entries older installs left in every recorded repo and its
// trees, replace the binary on PATH, and sweep the pins no one needs.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/host"
	"github.com/harryberg1n/trellis/internal/install"
	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
)

// Repo is the GitHub repository whose releases update installs.
const Repo = "harryberg1n/trellis"

// sumsAsset is the release asset that lists the sha256 of every binary.
const sumsAsset = "sha256sums.txt"

// smokeTimeout bounds the staged binary's `version`.
const smokeTimeout = 10 * time.Second

// Env is what update takes from the process.
type Env struct {
	Runner run.Runner      // runs gh and the staged binary
	Ctx    context.Context // cancelling it ends the running child; nil is never cancelled
	Out    io.Writer
	Root   string // the state root
	// ConfigDir is the Claude config directory whose settings.json holds the
	// hooks update points at the new pin.
	ConfigDir string
	Exe       string // the running binary, the one on PATH
	Version   string // buildinfo.Version of that binary
	GOOS      string
	GOARCH    string
	// Clock dates the stage directories; nil is the wall clock.
	Clock clock.Clock
	// LookPath finds a program on PATH; nil is exec.LookPath.
	LookPath func(file string) (string, error)
}

// Options are the flags of the verb.
type Options struct {
	To       string // switch to this version instead of the newest release
	Dry      bool   // print each step with "would " and change nothing
	AllowDev bool   // let a build whose version is "dev" update

	ForgetMissing bool // drop a recorded repo that is not found from the record
}

// Run updates. It prints one line per step; with Dry the lines start "would "
// and nothing is changed. A repo that cannot be re-installed is reported and
// the rest go on; Run then returns an error.
func Run(env Env, o Options) error {
	if env.Version == "" || (env.Version == "dev" && !o.AllowDev) {
		return errors.New("refusing to update an unversioned build; pass --allow-dev")
	}
	env.Runner = run.WithContext(env.Runner, env.Ctx)
	u := updater{Env: env, dry: o.Dry, gh: host.GH{Runner: env.Runner}}
	if !o.Dry {
		removeStale(env.Exe)
	}
	tag, err := u.target(o.To)
	if err != nil {
		return flat(err)
	}
	u.sweepStages() // before any return: a re-run that finds nothing to do sweeps too
	if tag == env.Version || (o.To == "" && compareTags(tag, env.Version) < 0) {
		u.say("[skip] already at %s", env.Version)
		return u.refresh(o.ForgetMissing)
	}
	bin, err := u.pin(tag)
	if err != nil {
		return flat(err)
	}
	if err := u.rewire(tag, bin, false); err != nil {
		return flat(err)
	}
	missing, tried, failed, err := u.prune(o.ForgetMissing)
	if err != nil {
		return flat(err)
	}
	pathErr := u.replaceOnPath(bin)
	u.sweep(tag, sweepFacts{missing: missing, failed: failed})
	u.step("update: %s -> %s", env.Version, tag)
	if pathErr != nil {
		u.say("the binary on PATH was not replaced: %s", oneLine(pathErr))
		return flat(pathErr)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d repos could not be updated", failed, tried)
	}
	return nil
}

// flat is err on one line.
func flat(err error) error {
	return errors.New(oneLine(err))
}

// maxLine is the longest line oneLine returns, in runes.
const maxLine = 200

// oneLine is err on one line of at most maxLine runes, cut with an ellipsis.
func oneLine(err error) string {
	text := strings.Join(strings.Fields(err.Error()), " ")
	if r := []rune(text); len(r) > maxLine {
		return string(r[:maxLine-1]) + "…"
	}
	return text
}

type updater struct {
	Env
	dry bool
	gh  host.GH
}

// say prints one line as it is.
func (u updater) say(format string, args ...any) {
	_, _ = fmt.Fprintf(u.Out, format+"\n", args...)
}

// step prints one action, "would " first in a dry run.
func (u updater) step(format string, args ...any) {
	if u.dry {
		format = "would " + format
	}
	u.say(format, args...)
}

func (u updater) ext() string {
	if u.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func (u updater) asset() string { return "trellis-" + u.GOOS + "-" + u.GOARCH + u.ext() }

// target is the release tag to move to: --to, or the newest release.
func (u updater) target(to string) (string, error) {
	tag := to
	switch {
	case tag == "":
		var err error
		if tag, err = u.gh.LatestRelease(Repo); err != nil {
			return "", err
		}
	case tag[0] >= '0' && tag[0] <= '9':
		tag = "v" + tag
	}
	if _, ok := parseTag(tag); !ok {
		return "", fmt.Errorf("%q is not a release tag (v<MAJOR>.<MINOR>.<PATCH>)", tag)
	}
	return tag, nil
}

// pin makes sure the pinned binary of tag exists, downloading it if not, and
// returns its path.
func (u updater) pin(tag string) (string, error) {
	bin := install.BinPath(u.Root, tag, u.ext())
	if _, err := os.Stat(bin); err == nil {
		u.say("keep pin %s", bin)
		return bin, nil
	}
	stage := filepath.Join(u.Root, "stage", tag)
	if u.dry {
		u.step("download %s and %s from %s into %s", u.asset(), sumsAsset, tag, stage)
		u.step("verify its sha256 and smoke-check the staged binary")
		u.step("pin %s", bin)
		return bin, nil
	}
	staged, err := u.fetch(tag, stage)
	defer func() { _ = os.RemoveAll(stage) }()
	if err != nil {
		return "", err
	}
	pinned, copied, err := install.Pin(u.Root, tag, staged)
	if err != nil {
		return "", err
	}
	if copied {
		// After the pin, so a crash between the two leaves a pin no sweep takes.
		if err := install.RecordCreatedPin(u.Root, tag); err != nil {
			return "", err
		}
	}
	u.say("pin %s", pinned)
	return pinned, nil
}

// fetch downloads the release asset and its checksums into stage, verifies the
// one against the other and runs the binary once.
func (u updater) fetch(tag, stage string) (string, error) {
	if err := os.RemoveAll(stage); err != nil {
		return "", err
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return "", err
	}
	if err := u.gh.DownloadRelease(Repo, tag, stage, u.asset(), sumsAsset); err != nil {
		return "", err
	}
	staged := filepath.Join(stage, u.asset())
	if err := verify(staged, filepath.Join(stage, sumsAsset), u.asset()); err != nil {
		return "", err
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		return "", err
	}
	if err := u.smoke(staged, tag); err != nil {
		return "", err
	}
	return staged, nil
}

// verify checks the sha256 of the file at path against name's line in sums.
func verify(path, sums, name string) error {
	list, err := os.ReadFile(sums)
	if err != nil {
		return err
	}
	want := ""
	for line := range strings.Lines(string(list)) {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = f[0]
		}
	}
	if want == "" {
		return fmt.Errorf("%s has no line for %s", sumsAsset, name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: %s says %s, the download is %s", name, sumsAsset, want, got)
	}
	return nil
}

// smoke runs the staged binary's `version` and requires it to name the tag.
func (u updater) smoke(staged, tag string) error {
	res, err := u.Runner.Light(run.Spec{Name: staged, Args: []string{"version"}, Timeout: smokeTimeout})
	if errors.Is(err, run.ErrTimeout) {
		return &timeoutError{msg: fmt.Sprintf("the staged binary failed its smoke check: timed out after %ds", int(smokeTimeout.Seconds()))}
	}
	if err != nil {
		return fmt.Errorf("the staged binary failed its smoke check: %s", strings.Join(strings.Fields(err.Error()), " "))
	}
	if f := strings.Fields(string(res.Stdout)); len(f) < 2 || f[1] != tag {
		return fmt.Errorf("the staged binary failed its smoke check: `version` printed %q, want it to name %s",
			strings.TrimSpace(string(res.Stdout)), tag)
	}
	return nil
}

// refresh is the work of an update that finds itself at the target version:
// the user-level hooks are pointed at the running version if they are not
// already, and every recorded repo is pruned of what an older install left, so
// a step that failed last time is not left behind.
func (u updater) refresh(forget bool) error {
	bin := install.BinPath(u.Root, u.Version, filepath.Ext(u.Exe))
	if _, err := os.Stat(bin); err != nil {
		bin = u.Exe // Wire pins the running binary when its pin is gone
	}
	if err := u.rewire(u.Version, bin, true); err != nil {
		return flat(err)
	}
	_, tried, failed, err := u.prune(forget)
	if err != nil {
		return flat(err)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d repos could not be updated", failed, tried)
	}
	return nil
}

// repoProblem is why a recorded path is not the repository it was: "" when it
// is one, else the reason. The path must still be the root of a repository, not
// just exist: a directory that lost its .git must not get a .claude of its own.
func repoProblem(repo string) string {
	if _, err := os.Stat(repo); err != nil {
		return "not found"
	}
	r, err := objects.Open(repo)
	if err != nil || store.Canonical(r.Root) != store.Canonical(repo) {
		return "not a repository root"
	}
	return ""
}

// rewire points the six user-level hook entries at the pinned binary of tag.
// A quiet rewire that changes nothing says nothing. A file that holds no
// trellis entry is not wired: it is not where this box installed, and update
// never installs; it says so and goes on.
func (u updater) rewire(tag, bin string, quiet bool) error {
	file := install.SettingsFile(u.ConfigDir)
	refs, err := install.ReferencedVersions(u.Root, u.ConfigDir)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		u.step("skip rewire %s: no trellis hooks there; trellis install [--config-dir D] wires them", file)
		return nil
	}
	if u.dry {
		u.step("rewire %s to %s", file, tag)
		return nil
	}
	opts := install.Options{ConfigDir: u.ConfigDir, Root: u.Root, Exe: bin, Version: tag}
	actions, err := install.Wire(opts)
	if err != nil {
		return err
	}
	n := hooksChanged(actions)
	if n > 0 || !quiet {
		u.say("rewire %s: %d hooks updated", file, n)
	}
	for _, problem := range install.Doctor(opts) {
		u.say("doctor %s: %s", file, problem)
	}
	return nil
}

// prune removes trellis's entries from the project-local settings of every
// recorded repo and its linked worktrees: the user-level file wires the hooks
// now, and an entry left there fires every event twice. It returns the repos it
// did not find, how many it tried and how many of those failed. A repo that is
// not found stays in the record unless forget says to drop it: an unmounted
// drive or a moved checkout may still hold entries that point at a pin.
func (u updater) prune(forget bool) (missing []string, tried, failed int, err error) {
	repos, err := install.Repos(u.Root)
	if err != nil {
		return nil, 0, 0, err
	}
	for _, repo := range repos {
		if why := repoProblem(repo); why != "" {
			if !forget {
				u.step("skip %s: %s", repo, why)
				missing = append(missing, repo)
				continue
			}
			u.step("forget %s: %s", repo, why)
			if !u.dry {
				if err := install.ForgetRepo(u.Root, repo); err != nil {
					return nil, 0, 0, err
				}
			}
			continue
		}
		tried++
		actions, err := install.Prune(install.Options{Repo: repo, Root: u.Root, Dry: u.dry})
		if err != nil {
			u.say("prune %s: %s", repo, oneLine(err))
			failed++
			continue
		}
		for _, a := range actions {
			u.say("%s", a)
		}
	}
	return missing, tried, failed, nil
}

// hooksChanged counts the actions that added or rewrote a hook; a hook that
// was already right is "keep" and not counted.
func hooksChanged(actions []string) int {
	n := 0
	for _, a := range actions {
		if strings.HasPrefix(a, "add hook ") || strings.HasPrefix(a, "update hook ") {
			n++
		}
	}
	return n
}

// replaceOnPath replaces the running binary with the pinned one, or says
// where each is when it cannot.
func (u updater) replaceOnPath(bin string) error {
	if u.Exe == "" {
		return nil
	}
	// Both sides in one spelling: os.Executable and the state root may differ
	// (8.3 short names, symlinks) for the same file.
	exe, pin := store.Canonical(u.Exe), store.Canonical(bin)
	if _, inside := store.Within(store.Canonical(filepath.Join(u.Root, "bin")), exe); inside {
		// A pinned copy is what the hooks run; it is not the one on PATH.
		u.say("the binary on PATH is %s; the new one is %s", exe, pin)
		return nil
	}
	if u.dry {
		u.step("replace %s with %s", u.Exe, bin)
		return nil
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		return err
	}
	switch err := replaceExe(u.Exe, data); {
	case errors.Is(err, errNotWritable):
		u.say("the binary on PATH is %s; the new one is %s", exe, pin)
	case err != nil:
		return err
	default:
		u.say("replace %s", u.Exe)
		u.checkPath()
	}
	return nil
}

// sweepFacts is what the sweep knows about the recorded repos.
type sweepFacts struct {
	missing []string // recorded repos that were not found
	failed  int      // repos whose project-local settings could not be pruned
}

// sweep removes the pinned versions beyond the newest three that the user-level
// hooks do not point at. A repo that is not found, or whose settings could not
// be pruned, may still hold entries that point at any pin: the sweep stops.
func (u updater) sweep(tag string, facts sweepFacts) {
	keep := map[string]bool{tag: true, u.Version: true}
	pins := append(pinned(u.Root), tag)
	madeByUpdate, err := install.CreatedPins(u.Root)
	if err != nil {
		u.say("sweep: %s", oneLine(err))
		return
	}
	created := map[string]bool{}
	for _, v := range madeByUpdate {
		created[v] = true
	}
	// Only a pin update created is a candidate: one an install made may be
	// pointed at by a repo the record never heard of.
	if len(sweepPlan(pins, keep, created)) == 0 {
		return
	}
	// A repo not found cannot be read, so what it references is unknown.
	if len(facts.missing) > 0 {
		u.say("sweep skipped: %s is not found; --forget-missing drops it from the record", facts.missing[0])
		return
	}
	if facts.failed > 0 {
		u.say("sweep skipped: %d repos could not be pruned, their hooks may point at any pin", facts.failed)
		return
	}
	// The file is only read, so a dry run reads it too.
	refs, err := install.ReferencedVersions(u.Root, u.ConfigDir)
	if err != nil {
		u.say("sweep: %s", oneLine(err))
		return
	}
	for _, r := range refs {
		keep[r] = true
	}
	for _, v := range sweepPlan(pins, keep, created) {
		if u.dry {
			u.step("remove pin %s", v)
			continue
		}
		if err := os.RemoveAll(filepath.Join(u.Root, "bin", v)); err != nil {
			u.say("keep pin %s: in use: %s", v, oneLine(err))
			continue
		}
		u.say("remove pin %s", v)
	}
}

// pinned lists the version directories under <root>/bin.
func pinned(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "bin"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// checkPath says so when the trellis a shell runs is not the file just
// replaced: the update then changed a binary nobody types. A process keeps the
// PATH it started with, which is also what the next shell is given unless the
// shell's startup files change it. Source: aphrollo-tools
// internal/userbin/launcher.go at 12b3724e (PathCheck, pathVerdict); incident
// pathcheck-new-shell, an update that printed success while `aphrollo` kept
// resolving to the old install.
func (u updater) checkPath() {
	lookPath := u.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	fix := "put " + filepath.Dir(u.Exe) + " first on PATH"
	found, err := lookPath("trellis")
	if err != nil {
		u.say("trellis is not on PATH: %s", fix)
		return
	}
	if !sameFile(found, u.Exe) {
		u.say("trellis on PATH resolves to %s, not %s: %s", found, u.Exe, fix)
	}
}

// sameFile reports whether a and b are the same file on disk.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

// staleStageAge is how old a staging directory must be before an update
// removes it: a younger one may be another update's, running now.
const staleStageAge = 24 * time.Hour

// sweepStages removes the staging directories under <root>/stage that are older
// than a day: an interrupted update leaves its own, and only the next update to
// the same tag would have cleared it. Source: aphrollo-tools
// internal/cli/update_userspace.go at 12b3724e (sweepStageDirs). A dry run says
// what it would remove.
func (u updater) sweepStages() {
	clk := u.Clock
	if clk == nil {
		clk = clock.Real()
	}
	entries, err := os.ReadDir(filepath.Join(u.Root, "stage"))
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || clk.Now().Sub(info.ModTime()) <= staleStageAge {
			continue
		}
		path := filepath.Join(u.Root, "stage", e.Name())
		if u.dry {
			u.step("remove stale stage %s", path)
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			u.say("keep stale stage %s: %s", path, oneLine(err))
			continue
		}
		u.say("remove stale stage %s", path)
	}
}

// timeoutError is a one-line message that still is run.ErrTimeout.
type timeoutError struct{ msg string }

func (e *timeoutError) Error() string { return e.msg }

func (*timeoutError) Unwrap() error { return run.ErrTimeout }
