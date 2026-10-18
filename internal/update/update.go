// Package update is `trellis update`: fetch a release of trellis, verify it,
// pin it under the state root, point every recorded repo's hooks at it, replace
// the binary on PATH, and sweep the pins no one needs.
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

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
	Runner  run.Runner // runs gh and the staged binary
	Out     io.Writer
	Root    string // the state root
	Exe     string // the running binary, the one on PATH
	Version string // buildinfo.Version of that binary
	GOOS    string
	GOARCH  string
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
	u := updater{Env: env, dry: o.Dry, gh: host.GH{Runner: env.Runner}}
	if !o.Dry {
		removeStale(env.Exe)
	}
	tag, err := u.target(o.To)
	if err != nil {
		return flat(err)
	}
	if tag == env.Version || (o.To == "" && compareTags(tag, env.Version) < 0) {
		u.say("[skip] already at %s", env.Version)
		return u.refresh(o.ForgetMissing)
	}
	bin, err := u.pin(tag)
	if err != nil {
		return flat(err)
	}
	alive, missing, tried, failed, err := u.reinstall(tag, bin, o.ForgetMissing, false)
	if err != nil {
		return flat(err)
	}
	pathErr := u.replaceOnPath(bin)
	u.sweep(tag, sweepFacts{alive: alive, missing: missing, forget: o.ForgetMissing})
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
// every recorded repo whose hooks do not all point at the running version is
// re-installed, so a repo that failed last time is not left behind.
func (u updater) refresh(forget bool) error {
	bin := install.BinPath(u.Root, u.Version, filepath.Ext(u.Exe))
	if _, err := os.Stat(bin); err != nil {
		bin = u.Exe // Install pins the running binary when its pin is gone
	}
	_, _, tried, failed, err := u.reinstall(u.Version, bin, forget, true)
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

// staleRefs says what the hooks of repo point at when they are not all the
// running version, "" when they are.
func staleRefs(root, repo, version string) string {
	refs, err := install.ReferencedVersions(root, repo)
	switch {
	case err != nil:
		return "unreadable settings"
	case len(refs) == 1 && refs[0] == version:
		return ""
	case len(refs) == 0:
		return "no hooks"
	}
	return strings.Join(refs, ", ")
}

// reinstall points every recorded repo's hooks at the pinned binary of tag.
// It returns the repos it found, the ones it did not, how many it tried and how
// many of those failed. A repo that is not found stays in the record unless
// forget says to drop it: an unmounted drive or a moved checkout still has
// hooks that point at a pin. With staleOnly a repo whose hooks already point
// at tag alone is left as it is.
func (u updater) reinstall(tag, bin string, forget, staleOnly bool) (alive, missing []string, tried, failed int, err error) {
	repos, err := install.Repos(u.Root)
	if err != nil {
		return nil, nil, 0, 0, err
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
					return nil, nil, 0, 0, err
				}
			}
			continue
		}
		alive = append(alive, repo)
		if staleOnly {
			stale := staleRefs(u.Root, repo, tag)
			if stale == "" {
				continue
			}
			u.step("reinstall %s: hooks point at %s, running %s", repo, stale, tag)
		}
		tried++
		if u.dry {
			u.step("install %s", repo)
			continue
		}
		opts := install.Options{Repo: repo, Root: u.Root, Exe: bin, Version: tag}
		actions, err := install.Install(opts)
		if err != nil {
			u.say("install %s: %s", repo, oneLine(err))
			failed++
			continue
		}
		u.say("install %s: %d hooks updated", repo, hooksChanged(actions))
		for _, problem := range install.Doctor(opts) {
			u.say("doctor %s: %s", repo, problem)
		}
	}
	return alive, missing, tried, failed, nil
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
	}
	return nil
}

// sweepFacts is what the sweep knows about the recorded repos.
type sweepFacts struct {
	alive, missing []string
	forget         bool // --forget-missing: drop what is not found
}

// sweep removes the pinned versions beyond the newest three that no live repo's
// hooks point at. A repo whose settings cannot be read stops the sweep: its
// hooks might point at anything.
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
	// Primary checkouts recorded as referenced only: update never installs into
	// them, but a linked worktree of theirs may point at a pin.
	readable := facts.alive
	only, err := install.ReferencedRepos(u.Root)
	if err != nil {
		u.say("sweep: %s", oneLine(err))
		return
	}
	for _, repo := range only {
		if slices.Contains(readable, repo) {
			continue
		}
		if why := repoProblem(repo); why != "" {
			if facts.forget {
				u.step("forget %s: %s", repo, why)
				if !u.dry {
					_ = install.ForgetReferenced(u.Root, repo) // a record left is read again next time
				}
				continue
			}
			u.say("sweep skipped: %s is not found; --forget-missing drops it from the record", repo)
			return
		}
		readable = append(readable, repo)
	}
	// ReferencedVersions only reads, so a dry run reads it too.
	for _, repo := range readable {
		refs, err := install.SweepReferences(u.Root, repo)
		if err != nil {
			u.say("sweep: %s: %s", repo, oneLine(err))
			return
		}
		for _, r := range refs {
			keep[r] = true
		}
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
