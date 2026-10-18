package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/harryberg1n/trellis/internal/install"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/testiso"
	"github.com/harryberg1n/trellis/internal/testsupport"
)

const (
	asset     = "trellis-linux-amd64"
	newBinary = "new-binary-bytes"
	oldBinary = "old-binary-bytes"
)

// world is a state root with two installed repos at v0.0.3, a binary "on PATH",
// and a fake gh that serves release assets from a directory.
type world struct {
	t      *testing.T
	dir    string
	root   string
	repos  []string
	exe    string
	assets string // what `gh release download` copies from
	latest string // the tag `gh api` reports
	smoke  func(run.Spec) (run.Result, error)
	runner *testsupport.FakeRunner
	out    *bytes.Buffer
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{t: t, dir: dir, root: filepath.Join(dir, "state"), assets: filepath.Join(dir, "assets"), latest: "v0.0.4", out: &bytes.Buffer{}}
	src := filepath.Join(dir, "src", "trellis")
	testiso.Write(t, src, "v0.0.3-binary")
	for range 2 {
		repo := testiso.Repo(t)
		if _, err := install.Install(install.Options{Repo: repo, Root: w.root, Exe: src, Version: "v0.0.3"}); err != nil {
			t.Fatal(err)
		}
		w.repos = append(w.repos, store.Canonical(repo))
	}
	w.exe = filepath.Join(dir, "path", "trellis")
	testiso.Write(t, w.exe, oldBinary)
	w.serve(newBinary)
	w.smoke = func(run.Spec) (run.Result, error) {
		return run.Result{Stdout: []byte("trellis v0.0.4 (abc1234)\n")}, nil
	}
	w.runner = &testsupport.FakeRunner{Answer: w.answer}
	return w
}

// serve puts the release assets for a binary with these bytes in w.assets.
func (w *world) serve(binary string) {
	sum := sha256.Sum256([]byte(binary))
	w.serveSums(hex.EncodeToString(sum[:]) + "  " + asset + "\n")
	testiso.Write(w.t, filepath.Join(w.assets, asset), binary)
}

func (w *world) serveSums(sums string) {
	testiso.Write(w.t, filepath.Join(w.assets, "sha256sums.txt"), sums)
}

// answer is gh, and the staged binary when the update runs it.
func (w *world) answer(spec run.Spec) (run.Result, error) {
	if spec.Name != "gh" {
		return w.smoke(spec)
	}
	switch spec.Args[0] {
	case "api":
		return run.Result{Stdout: []byte(`{"tag_name":"` + w.latest + `"}`)}, nil
	case "release":
		var dir string
		var names []string
		for i, a := range spec.Args {
			switch a {
			case "--dir":
				dir = spec.Args[i+1]
			case "--pattern":
				names = append(names, spec.Args[i+1])
			}
		}
		for _, n := range names {
			b, err := os.ReadFile(filepath.Join(w.assets, n))
			if err != nil {
				return run.Result{}, err
			}
			if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
				return run.Result{}, err
			}
		}
	}
	return run.Result{}, nil
}

func (w *world) env() Env {
	return Env{Runner: w.runner, Out: w.out, Root: w.root, Exe: w.exe, Version: "v0.0.3", GOOS: "linux", GOARCH: "amd64"}
}

func (w *world) run(o Options) error { return Run(w.env(), o) }

func (w *world) lines() []string {
	return strings.Split(strings.TrimSuffix(w.out.String(), "\n"), "\n")
}

func (w *world) pinned() []string {
	entries, err := os.ReadDir(filepath.Join(w.root, "bin"))
	if err != nil {
		w.t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func (w *world) referenced(repo string) []string {
	w.t.Helper()
	got, err := install.ReferencedVersions(w.root, repo)
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdate_DownloadsVerifiesPinsAndReinstallsEveryRepo(t *testing.T) {
	w := newWorld(t)

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	bin := install.BinPath(w.root, "v0.0.4", "")
	if got := read(t, bin); got != newBinary {
		t.Errorf("pinned binary = %q, want %q", got, newBinary)
	}
	if info, err := os.Stat(bin); err != nil || (info.Mode().Perm()&0o100 == 0 && runtime.GOOS != "windows") {
		t.Errorf("pinned binary is not executable: %v, %v", info, err)
	}
	for _, repo := range w.repos {
		if got := w.referenced(repo); !reflect.DeepEqual(got, []string{"v0.0.4"}) {
			t.Errorf("%s hooks point at %v, want [v0.0.4]", repo, got)
		}
		if want := "install " + repo + ": 6 hooks updated"; !contains(w.lines(), want) {
			t.Errorf("no line %q in:\n%s", want, w.out)
		}
	}
	if got := w.lines(); got[len(got)-1] != "update: v0.0.3 -> v0.0.4" {
		t.Errorf("last line = %q, want %q", got[len(got)-1], "update: v0.0.3 -> v0.0.4")
	}
	if got := read(t, w.exe); got != newBinary {
		t.Errorf("the binary on PATH = %q, want %q", got, newBinary)
	}
	if got := read(t, filepath.Join(w.root, "pins.txt")); got != "v0.0.4\n" {
		t.Errorf("pins.txt = %q, want the pin update created", got)
	}
	if _, err := os.Stat(filepath.Join(w.root, "stage", "v0.0.4")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the staging directory was left: %v", err)
	}

	specs := w.runner.Specs()
	if len(specs) != 3 {
		t.Fatalf("%d children, want 3 (latest, download, smoke): %+v", len(specs), specs)
	}
	wantDownload := []string{
		"release", "download", "v0.0.4", "--repo", "harryberg1n/trellis", "--dir", filepath.Join(w.root, "stage", "v0.0.4"),
		"--clobber", "--pattern", asset, "--pattern", "sha256sums.txt",
	}
	if !reflect.DeepEqual(specs[1].Args, wantDownload) {
		t.Errorf("download argv = %v, want %v", specs[1].Args, wantDownload)
	}
	if specs[2].Name != filepath.Join(w.root, "stage", "v0.0.4", asset) || !reflect.DeepEqual(specs[2].Args, []string{"version"}) {
		t.Errorf("smoke check ran %s %v", specs[2].Name, specs[2].Args)
	}
}

func TestUpdate_NamesTheAssetForThePlatform(t *testing.T) {
	w := newWorld(t)
	env := w.env()
	env.GOOS, env.GOARCH = "windows", "arm64"
	if err := os.Rename(filepath.Join(w.assets, asset), filepath.Join(w.assets, "trellis-windows-arm64.exe")); err != nil {
		t.Fatal(err)
	}
	w.serveSums(strings.Replace(read(t, filepath.Join(w.assets, "sha256sums.txt")), asset, "trellis-windows-arm64.exe", 1))

	if err := Run(env, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, install.BinPath(w.root, "v0.0.4", ".exe")); got != newBinary {
		t.Errorf("pinned trellis.exe = %q, want %q", got, newBinary)
	}
}

func TestUpdate_RefusesChecksumMismatch(t *testing.T) {
	w := newWorld(t)
	w.serveSums(strings.Repeat("0", 64) + "  " + asset + "\n")

	err := w.run(Options{})

	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("err = %v, want one line naming a checksum mismatch", err)
	}
	w.assertUntouched()
}

func TestUpdate_RefusesAStagedBinaryThatFailsItsSmokeCheck(t *testing.T) {
	for name, c := range map[string]struct {
		stdout string
		err    error
	}{
		"wrong tag": {stdout: "trellis v0.0.9 (abc)\n"},
		"fails":     {err: errors.New("exit status 1")},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.smoke = func(run.Spec) (run.Result, error) { return run.Result{Stdout: []byte(c.stdout)}, c.err }

			err := w.run(Options{})

			if err == nil || !strings.Contains(err.Error(), "smoke check") {
				t.Fatalf("err = %v, want a smoke check refusal", err)
			}
			w.assertUntouched()
		})
	}
}

// assertUntouched checks that a refused update left v0.0.3 everywhere.
func (w *world) assertUntouched() {
	w.t.Helper()
	if got := w.pinned(); !reflect.DeepEqual(got, []string{"v0.0.3"}) {
		w.t.Errorf("pins = %v, want [v0.0.3]", got)
	}
	for _, repo := range w.repos {
		if got := w.referenced(repo); !reflect.DeepEqual(got, []string{"v0.0.3"}) {
			w.t.Errorf("%s hooks point at %v, want [v0.0.3]", repo, got)
		}
	}
	if got := read(w.t, w.exe); got != oldBinary {
		w.t.Errorf("the binary on PATH = %q, want it untouched", got)
	}
	if _, err := os.Stat(filepath.Join(w.root, "stage", "v0.0.4")); !errors.Is(err, fs.ErrNotExist) {
		w.t.Errorf("the staging directory was left: %v", err)
	}
}

func TestUpdate_SkipsWhenAlreadyCurrent(t *testing.T) {
	w := newWorld(t)
	w.latest = "v0.0.3"
	testiso.Write(t, w.exe+".old", "stale")

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	if got := w.out.String(); got != "[skip] already at v0.0.3\n" {
		t.Errorf("output = %q, want the skip line", got)
	}
	if n := len(w.runner.Specs()); n != 1 {
		t.Errorf("%d children, want only the latest-release lookup", n)
	}
	if _, err := os.Stat(w.exe + ".old"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a stale .old binary was left: %v", err)
	}
}

func TestUpdate_ToSwitchesWithoutDownload(t *testing.T) {
	w := newWorld(t)
	src := filepath.Join(w.dir, "src2", "trellis")
	testiso.Write(t, src, "v0.0.2-binary")
	if _, _, err := install.Pin(w.root, "v0.0.2", src); err != nil {
		t.Fatal(err)
	}

	if err := w.run(Options{To: "v0.0.2"}); err != nil {
		t.Fatal(err)
	}

	if n := len(w.runner.Specs()); n != 0 {
		t.Errorf("%d children ran, want none: %+v", n, w.runner.Specs())
	}
	for _, repo := range w.repos {
		if got := w.referenced(repo); !reflect.DeepEqual(got, []string{"v0.0.2"}) {
			t.Errorf("%s hooks point at %v, want [v0.0.2]", repo, got)
		}
	}
	if got := read(t, w.exe); got != "v0.0.2-binary" {
		t.Errorf("the binary on PATH = %q, want v0.0.2-binary", got)
	}
	if got := w.lines(); got[len(got)-1] != "update: v0.0.3 -> v0.0.2" {
		t.Errorf("last line = %q", got[len(got)-1])
	}
}

func TestUpdate_KeepsNewestThreePins(t *testing.T) {
	w := newWorld(t)
	w.updatePins("v0.0.1", "v0.0.2", "v0.0.10")
	w.oldPins("odd")

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	if got, want := w.pinned(), []string{"odd", "v0.0.10", "v0.0.3", "v0.0.4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pins = %v, want %v", got, want)
	}
	for _, want := range []string{"remove pin v0.0.1", "remove pin v0.0.2"} {
		if !contains(w.lines(), want) {
			t.Errorf("no line %q in:\n%s", want, w.out)
		}
	}
}

func TestSweepPlan_RemovesOnlyOldUnreferencedCreatedPins(t *testing.T) {
	pins := []string{"v0.0.1", "v0.0.2", "v0.0.3", "v0.0.4", "v0.0.10", "v0.0.10", "odd"}

	created := map[string]bool{"v0.0.1": true, "v0.0.2": true, "v0.0.4": true}
	got := sweepPlan(pins, map[string]bool{"v0.0.1": true}, created)

	if want := []string{"v0.0.2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sweepPlan removes %v, want %v", got, want)
	}
}

func TestUpdate_DryChangesNothing(t *testing.T) {
	w := newWorld(t)
	testiso.Write(t, w.exe+".old", "stale")
	before := hashTree(t, w.dir)

	if err := w.run(Options{Dry: true}); err != nil {
		t.Fatal(err)
	}

	if after := hashTree(t, w.dir); !reflect.DeepEqual(before, after) {
		t.Errorf("a dry update changed the files:\nbefore %v\nafter  %v", before, after)
	}
	if n := len(w.runner.Specs()); n != 1 {
		t.Errorf("%d children, want only the latest-release lookup", n)
	}
	for _, repo := range w.repos {
		if got := w.referenced(repo); !reflect.DeepEqual(got, []string{"v0.0.3"}) {
			t.Errorf("%s hooks point at %v after a dry run, want [v0.0.3]", repo, got)
		}
	}
	for _, l := range w.lines() {
		if !strings.HasPrefix(l, "would ") {
			t.Errorf("dry line %q does not start with \"would \"", l)
		}
	}
	for _, want := range []string{
		"would pin " + install.BinPath(w.root, "v0.0.4", ""),
		"would install " + w.repos[0],
		"would install " + w.repos[1],
		"would replace " + w.exe + " with " + install.BinPath(w.root, "v0.0.4", ""),
		"would update: v0.0.3 -> v0.0.4",
	} {
		if !contains(w.lines(), want) {
			t.Errorf("no line %q in:\n%s", want, w.out)
		}
	}
}

func TestUpdate_KeepsAMissingRepoInTheRecord(t *testing.T) {
	w := newWorld(t)
	missing := filepath.Join(w.dir, "unmounted")
	if err := install.RecordRepo(w.root, missing); err != nil {
		t.Fatal(err)
	}

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	if want := "skip " + store.Canonical(missing) + ": not found"; !contains(w.lines(), want) {
		t.Errorf("no line %q in:\n%s", want, w.out)
	}
	got, err := install.Repos(w.root)
	if want := append(append([]string(nil), w.repos...), store.Canonical(missing)); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Repos = %v, %v; want %v", got, err, want)
	}
}

func TestUpdate_ForgetMissingDropsAMissingRepo(t *testing.T) {
	w := newWorld(t)
	missing := filepath.Join(w.dir, "gone")
	if err := install.RecordRepo(w.root, missing); err != nil {
		t.Fatal(err)
	}

	if err := w.run(Options{ForgetMissing: true}); err != nil {
		t.Fatal(err)
	}

	if want := "forget " + store.Canonical(missing) + ": not found"; !contains(w.lines(), want) {
		t.Errorf("no line %q in:\n%s", want, w.out)
	}
	got, err := install.Repos(w.root)
	if err != nil || !reflect.DeepEqual(got, w.repos) {
		t.Errorf("Repos = %v, %v; want %v", got, err, w.repos)
	}
}

// oldPins pins these versions with some bytes.
func (w *world) oldPins(versions ...string) {
	w.t.Helper()
	src := filepath.Join(w.dir, "older", "trellis")
	testiso.Write(w.t, src, "older")
	for _, v := range versions {
		if _, _, err := install.Pin(w.root, v, src); err != nil {
			w.t.Fatal(err)
		}
	}
}

// updatePins pins these versions the way update does: the file, then a line in
// pins.txt.
func (w *world) updatePins(versions ...string) {
	w.t.Helper()
	w.oldPins(versions...)
	for _, v := range versions {
		if err := install.RecordCreatedPin(w.root, v); err != nil {
			w.t.Fatal(err)
		}
	}
}

// alsoReference adds a second Stop hook to the repo's settings that runs the
// pinned binary of version, a place install's re-install does not rewrite.
func (w *world) alsoReference(repo, version string) {
	w.t.Helper()
	path := install.SettingsPath(repo)
	var doc map[string]any
	if err := json.Unmarshal([]byte(read(w.t, path)), &doc); err != nil {
		w.t.Fatal(err)
	}
	hooks := doc["hooks"].(map[string]any)
	hooks["Stop"] = append(hooks["Stop"].([]any), map[string]any{"hooks": []any{map[string]any{
		"type": "command", "command": install.Command(install.BinPath(w.root, version, ""), "Stop"),
	}}})
	b, err := json.Marshal(doc)
	if err != nil {
		w.t.Fatal(err)
	}
	testiso.Write(w.t, path, string(b))
}

func TestUpdate_SweepKeepsAPinARepoStillReferences(t *testing.T) {
	w := newWorld(t)
	w.updatePins("v0.0.1", "v0.0.2")
	w.oldPins("v0.0.5")
	w.alsoReference(w.repos[1], "v0.0.1")

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	if got, want := w.pinned(), []string{"v0.0.1", "v0.0.3", "v0.0.4", "v0.0.5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pins = %v, want %v (v0.0.1 is referenced, v0.0.2 is not)", got, want)
	}
	if !contains(w.lines(), "remove pin v0.0.2") {
		t.Errorf("no removal of v0.0.2 in:\n%s", w.out)
	}
}

func TestUpdate_NeverSweepsAPinItDidNotCreate(t *testing.T) {
	w := newWorld(t)
	w.oldPins("v0.0.1", "v0.0.2", "v0.0.5") // made by installs, not in pins.txt

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	if got, want := w.pinned(), []string{"v0.0.1", "v0.0.2", "v0.0.3", "v0.0.4", "v0.0.5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pins = %v, want %v", got, want)
	}
	for _, l := range w.lines() {
		if strings.Contains(l, "pin v0.0.1") || strings.HasPrefix(l, "sweep") {
			t.Errorf("unexpected line %q", l)
		}
	}
}

func TestUpdate_SweepSkippedWhileARepoIsMissing(t *testing.T) {
	w := newWorld(t)
	w.updatePins("v0.0.1", "v0.0.2")
	w.oldPins("v0.0.5")
	missing := store.Canonical(filepath.Join(w.dir, "unmounted"))
	if err := install.RecordRepo(w.root, missing); err != nil {
		t.Fatal(err)
	}

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	if got := w.pinned(); len(got) != 5 {
		t.Errorf("pins = %v, want nothing removed", got)
	}
	if want := "sweep skipped: " + missing + " is not found; --forget-missing drops it from the record"; !contains(w.lines(), want) {
		t.Errorf("no line %q in:\n%s", want, w.out)
	}
}

func TestUpdate_SweepStopsWhenASettingsFileIsUnreadable(t *testing.T) {
	w := newWorld(t)
	w.updatePins("v0.0.1", "v0.0.2")
	w.oldPins("v0.0.5")
	testiso.Write(t, install.SettingsPath(w.repos[0]), "{ not json")

	err := w.run(Options{})

	if err == nil {
		t.Fatal("a repo that could not be re-installed gave no error")
	}
	if got := w.pinned(); len(got) != 5 {
		t.Errorf("pins = %v, want nothing removed", got)
	}
	found := false
	for _, l := range w.lines() {
		found = found || strings.HasPrefix(l, "sweep: "+w.repos[0]+": ")
	}
	if !found {
		t.Errorf("no sweep line naming %s in:\n%s", w.repos[0], w.out)
	}
}

func TestUpdate_DryNamesExactlyThePinsARealRunRemoves(t *testing.T) {
	removed := func(dry bool) []string {
		w := newWorld(t)
		w.updatePins("v0.0.1", "v0.0.2", "v0.0.6")
		w.oldPins("v0.0.5", "v0.0.7")
		w.alsoReference(w.repos[1], "v0.0.1")
		if err := w.run(Options{Dry: dry}); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range w.lines() {
			if name, ok := strings.CutPrefix(strings.TrimPrefix(l, "would "), "remove pin "); ok {
				out = append(out, name)
			}
		}
		return out
	}

	dry, actual := removed(true), removed(false)

	if want := []string{"v0.0.2"}; !reflect.DeepEqual(dry, want) || !reflect.DeepEqual(actual, want) {
		t.Errorf("dry removes %v, actual removes %v; both want %v", dry, actual, want)
	}
}

func TestUpdate_ErrorsReachTheCallerOnOneLine(t *testing.T) {
	w := newWorld(t)
	w.runner = &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		return run.Result{}, errors.New("exit status 1:\nHTTP 404: Not Found\n  (second line)")
	}}

	err := w.run(Options{})

	if err == nil || strings.Contains(err.Error(), "\n") {
		t.Errorf("err = %q, want one line", err)
	}
}

func TestVerify_ReadsBinaryModeNamesAndUppercaseHex(t *testing.T) {
	dir := t.TempDir()
	testiso.Write(t, filepath.Join(dir, "bin"), "abc")
	// sha256 of "abc", as `sha256sum -b` prints it: a "*" before the name.
	const sum = "BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD"
	testiso.Write(t, filepath.Join(dir, "sums"), "0000  other\n"+sum+" *bin\n")

	if err := verify(filepath.Join(dir, "bin"), filepath.Join(dir, "sums"), "bin"); err != nil {
		t.Errorf("verify = %v, want nil", err)
	}
	if err := verify(filepath.Join(dir, "bin"), filepath.Join(dir, "sums"), "missing"); err == nil {
		t.Error("a name with no line verified")
	}
}

func TestUpdate_DoesNotDowngradeToAnOlderRelease(t *testing.T) {
	w := newWorld(t)
	w.latest = "v0.0.2"

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	if got := w.out.String(); got != "[skip] already at v0.0.3\n" {
		t.Errorf("output = %q, want the skip line", got)
	}
	if got := w.pinned(); !reflect.DeepEqual(got, []string{"v0.0.3"}) {
		t.Errorf("pins = %v, want [v0.0.3]", got)
	}
}

func TestUpdate_ReportsARepoItCannotReinstallAndGoesOn(t *testing.T) {
	w := newWorld(t)
	testiso.Write(t, install.SettingsPath(w.repos[0]), "{ not json")

	err := w.run(Options{})

	if err == nil || !strings.Contains(err.Error(), "1 of 2 repos") {
		t.Fatalf("err = %v, want a count of the repos that failed", err)
	}
	if got := w.referenced(w.repos[1]); !reflect.DeepEqual(got, []string{"v0.0.4"}) {
		t.Errorf("the healthy repo points at %v, want [v0.0.4]", got)
	}
	if got := read(t, w.exe); got != newBinary {
		t.Errorf("the binary on PATH = %q, want it replaced", got)
	}
}

func TestUpdate_RefusesADevBuildWithoutAllowDev(t *testing.T) {
	w := newWorld(t)
	env := w.env()
	env.Version = "dev"

	err := Run(env, Options{})

	if err == nil || !strings.Contains(err.Error(), "--allow-dev") {
		t.Fatalf("err = %v, want a refusal naming --allow-dev", err)
	}
	if n := len(w.runner.Specs()); n != 0 {
		t.Errorf("%d children ran before the refusal", n)
	}
	if err := Run(env, Options{AllowDev: true}); err != nil {
		t.Errorf("with --allow-dev: %v", err)
	}
}

func TestUpdate_ANonPermissionFailureOnPathIsAnError(t *testing.T) {
	w := newWorld(t)
	w.exe = filepath.Join(w.dir, "missing", "dir", "trellis")

	err := w.run(Options{})

	if err == nil {
		t.Fatal("a failed replace of the binary on PATH exited 0")
	}
	if strings.Contains(w.out.String(), "the binary on PATH is") {
		t.Errorf("a failure that is not a permission one printed the hint:\n%s", w.out)
	}
	lines := w.lines()
	if !contains(lines, "update: v0.0.3 -> v0.0.4") {
		t.Errorf("no summary line in:\n%s", w.out)
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "the binary on PATH was not replaced: ") {
		t.Errorf("last line = %q, want the reason the binary on PATH was not replaced", last)
	}
	for _, repo := range w.repos {
		if got := w.referenced(repo); !reflect.DeepEqual(got, []string{"v0.0.4"}) {
			t.Errorf("%s hooks point at %v, want [v0.0.4]", repo, got)
		}
	}
}

func TestUpdate_BinaryOnPathInsideThePinsIsNamedNotReplaced(t *testing.T) {
	w := newWorld(t)
	w.exe = install.BinPath(w.root, "v0.0.3", "")

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	want := "the binary on PATH is " + store.Canonical(w.exe) + "; the new one is " + store.Canonical(install.BinPath(w.root, "v0.0.4", ""))
	if !contains(w.lines(), want) {
		t.Errorf("no line %q in:\n%s", want, w.out)
	}
}

func TestClassify_OnlyPermissionIsNotWritable(t *testing.T) {
	if err := notWritable(fmt.Errorf("open: %w", fs.ErrPermission)); !errors.Is(err, errNotWritable) {
		t.Errorf("a permission error is %v, want not-writable", err)
	}
	if err := notWritable(syscall.EROFS); !errors.Is(err, errNotWritable) {
		t.Errorf("a read-only file system is %v, want not-writable", err)
	}
	disk := errors.New("no space left on device")
	if err := notWritable(disk); errors.Is(err, errNotWritable) || !errors.Is(err, disk) {
		t.Errorf("a full disk is %v, want the error itself", err)
	}
}

func TestUpdate_RefusesAnythingButAReleaseTag(t *testing.T) {
	w := newWorld(t)
	for _, to := range []string{"../x", "v1.2", "latest"} {
		if err := w.run(Options{To: to}); err == nil {
			t.Errorf("--to %q was accepted", to)
		}
	}
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// hashTree is the sha256 of every file under dir, keyed by path.
func hashTree(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[p+"/"] = [32]byte{}
			return nil
		}
		b, err := os.ReadFile(p)
		out[p] = sha256.Sum256(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// installAt points a repo's hooks at a pinned version.
func (w *world) installAt(repo, version string) {
	w.t.Helper()
	src := filepath.Join(w.dir, "src-"+version, "trellis")
	testiso.Write(w.t, src, version+"-binary")
	if _, err := install.Install(install.Options{Repo: repo, Root: w.root, Exe: src, Version: version}); err != nil {
		w.t.Fatal(err)
	}
}

func TestUpdate_SkipStillReinstallsARepoPointingAtAnotherVersion(t *testing.T) {
	w := newWorld(t)
	w.latest = "v0.0.3"
	w.installAt(w.repos[0], "v0.0.2")

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	if got := w.referenced(w.repos[0]); !reflect.DeepEqual(got, []string{"v0.0.3"}) {
		t.Errorf("%s hooks point at %v, want [v0.0.3]", w.repos[0], got)
	}
	lines := w.lines()
	if lines[0] != "[skip] already at v0.0.3" {
		t.Errorf("first line = %q, want the skip line", lines[0])
	}
	if want := "reinstall " + w.repos[0] + ": hooks point at v0.0.2, running v0.0.3"; !contains(lines, want) {
		t.Errorf("no line %q in:\n%s", want, w.out)
	}
	if want := "install " + w.repos[0] + ": 6 hooks updated"; !contains(lines, want) {
		t.Errorf("no line %q in:\n%s", want, w.out)
	}
	for _, l := range lines {
		if strings.Contains(l, w.repos[1]) {
			t.Errorf("the repo already at v0.0.3 was touched: %q", l)
		}
	}
}

func TestUpdate_SkipReportsARepoThatFailsAgain(t *testing.T) {
	w := newWorld(t)
	w.latest = "v0.0.3"
	testiso.Write(t, install.SettingsPath(w.repos[0]), "{ not json")

	err := w.run(Options{})

	if err == nil || !strings.Contains(err.Error(), "1 of 1 repos") {
		t.Fatalf("err = %v, want the count of repos that failed", err)
	}
}

func TestUpdate_TreatsARecordedPlainDirectoryAsNotFound(t *testing.T) {
	w := newWorld(t)
	plain := filepath.Join(w.dir, "was-a-repo")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(w.repos[1], "subdir")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{plain, inside} {
		if err := install.RecordRepo(w.root, p); err != nil {
			t.Fatal(err)
		}
	}

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{plain, inside} {
		if _, err := os.Stat(filepath.Join(p, ".claude")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s got a .claude directory: %v", p, err)
		}
		if want := "skip " + store.Canonical(p) + ": not a repository root"; !contains(w.lines(), want) {
			t.Errorf("no line %q in:\n%s", want, w.out)
		}
	}
	got, err := install.Repos(w.root)
	if err != nil || len(got) != 4 {
		t.Errorf("Repos = %v, %v; want all four kept in the record", got, err)
	}
}

func TestUpdate_SweepKeepsAPinOnlyALinkedWorktreeReferences(t *testing.T) {
	w := newWorld(t)
	w.updatePins("v0.0.1", "v0.0.2")
	w.oldPins("v0.0.5")
	tree := testiso.AddTree(t, w.repos[0], "feat-tree", "feat")
	w.installAt(tree, "v0.0.1")

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	if got, want := w.pinned(), []string{"v0.0.1", "v0.0.3", "v0.0.4", "v0.0.5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pins = %v, want %v (v0.0.1 is the worktree's, v0.0.2 is nobody's)", got, want)
	}
}

func TestUpdate_SweepKeepsAPinAWorktreeOfAnUninstalledPrimaryReferences(t *testing.T) {
	w := newWorld(t)
	w.updatePins("v0.0.1", "v0.0.2")
	w.oldPins("v0.0.5")
	primary := testiso.Repo(t)
	tree := testiso.AddTree(t, primary, "other-tree", "other")
	w.installAt(tree, "v0.0.1")

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	if got, want := w.pinned(), []string{"v0.0.1", "v0.0.3", "v0.0.4", "v0.0.5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pins = %v, want %v", got, want)
	}
	if _, err := os.Stat(install.SettingsPath(primary)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("update installed into the referenced-only primary: %v", err)
	}
	if got, _ := install.Repos(w.root); len(got) != 2 {
		t.Errorf("Repos = %v, want the referenced-only primary kept out of it", got)
	}
}

func TestOneLine_TruncatesToTwoHundredRunesWithAnEllipsis(t *testing.T) {
	long := errors.New(strings.Repeat("é", 300))

	got := oneLine(long)

	if want := strings.Repeat("é", 199) + "…"; got != want {
		t.Errorf("oneLine = %d runes %q, want 199 runes and an ellipsis", len([]rune(got)), got)
	}
	if got := oneLine(errors.New("a\n  b")); got != "a b" {
		t.Errorf("oneLine(short) = %q, want %q", got, "a b")
	}
}

func TestHooksChanged_CountsAddedAndUpdatedNotKept(t *testing.T) {
	actions := []string{"keep binary /x", "add hook Stop", "update hook PreToolUse", "keep hook SessionEnd", "keep hook SessionStart"}

	if got := hooksChanged(actions); got != 2 {
		t.Errorf("hooksChanged = %d, want 2", got)
	}
}
