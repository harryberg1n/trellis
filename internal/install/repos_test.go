package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"

	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestInstall_RecordsTheRepo(t *testing.T) {
	f := newFixture(t)

	if _, err := Install(f.optsIn(true)); err != nil {
		t.Fatal(err)
	}
	if got, err := Repos(f.root); err != nil || len(got) != 0 {
		t.Fatalf("a dry install recorded %v, %v; want nothing", got, err)
	}

	for range 2 {
		if _, err := Install(f.optsIn(false)); err != nil {
			t.Fatal(err)
		}
	}
	want := store.Canonical(f.repo)
	got, err := Repos(f.root)
	if err != nil || !reflect.DeepEqual(got, []string{want}) {
		t.Fatalf("Repos = %v, %v; want [%s] once", got, err, want)
	}
	if file := readFile(t, filepath.Join(f.root, "repos.txt")); file != want+"\n" {
		t.Errorf("repos.txt = %q, want %q", file, want+"\n")
	}
}

func TestCreatedPins_RecordOncePerVersion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if got, err := CreatedPins(root); err != nil || len(got) != 0 {
		t.Fatalf("CreatedPins with no record = %v, %v; want none", got, err)
	}
	for _, v := range []string{"v0.0.4", "v0.0.5", "v0.0.4"} {
		if err := RecordCreatedPin(root, v); err != nil {
			t.Fatal(err)
		}
	}
	got, err := CreatedPins(root)
	if want := []string{"v0.0.4", "v0.0.5"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("CreatedPins = %v, %v; want %v", got, err, want)
	}
	if file := readFile(t, filepath.Join(root, "pins.txt")); file != "v0.0.4\nv0.0.5\n" {
		t.Errorf("pins.txt = %q", file)
	}
}

func TestPin_DoesNotRecordACreatedPin(t *testing.T) {
	f := newFixture(t)
	if _, _, err := Pin(f.root, "v0.0.9", f.exe); err != nil {
		t.Fatal(err)
	}
	if got, _ := CreatedPins(f.root); len(got) != 0 {
		t.Errorf("Pin recorded %v; only update records what it created", got)
	}
}

func TestEject_ForgetsTheRepo(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(t.TempDir(), "other")
	if err := RecordRepo(f.root, other); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(f.optsIn(false)); err != nil {
		t.Fatal(err)
	}

	if got, _ := Repos(f.root); len(got) != 2 {
		t.Fatalf("before eject Repos = %v, want 2", got)
	}

	if _, err := Eject(f.optsIn(false)); err != nil {
		t.Fatal(err)
	}
	got, err := Repos(f.root)
	if err != nil || !reflect.DeepEqual(got, []string{store.Canonical(other)}) {
		t.Fatalf("Repos after eject = %v, %v; want only %s", got, err, store.Canonical(other))
	}
}

func TestRepos_AbsentFileIsNoRepos(t *testing.T) {
	got, err := Repos(filepath.Join(t.TempDir(), "none"))
	if err != nil || len(got) != 0 {
		t.Errorf("Repos = %v, %v; want none", got, err)
	}
}

func TestReferencedVersions_ReadsWhatTheHooksPointAt(t *testing.T) {
	f := newFixture(t)
	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	got, err := ReferencedVersions(f.root, f.config)
	if err != nil || !reflect.DeepEqual(got, []string{"v0.0.2"}) {
		t.Errorf("ReferencedVersions = %v, %v; want [v0.0.2]", got, err)
	}
	if err := os.Remove(f.settings()); err != nil {
		t.Fatal(err)
	}
	if got, err := ReferencedVersions(f.root, f.config); err != nil || len(got) != 0 {
		t.Errorf("with no settings: %v, %v; want none", got, err)
	}
}

func TestPin_CopiesOnceAndKeepsAnExistingPin(t *testing.T) {
	f := newFixture(t)

	bin, copied, err := Pin(f.root, "v0.0.9", f.exe)
	if err != nil || !copied || bin != BinPath(f.root, "v0.0.9", filepath.Ext(f.exe)) {
		t.Fatalf("Pin = %q, %v, %v; want a copy at the pinned path", bin, copied, err)
	}
	if got := readFile(t, bin); got != "binary-bytes" {
		t.Errorf("pinned content = %q, want binary-bytes", got)
	}
	if err := os.WriteFile(bin, []byte("first"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, copied, err := Pin(f.root, "v0.0.9", f.exe); err != nil || copied {
		t.Errorf("second Pin copied=%v err=%v; want it kept", copied, err)
	}
	if got := readFile(t, bin); got != "first" {
		t.Errorf("an existing pin was overwritten: %q", got)
	}
	if _, _, err := Pin(f.root, "../x", f.exe); err == nil {
		t.Error("a version naming a parent directory was accepted")
	}
}

func TestRecordRepo_ParallelRecordsLoseNoLine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	repos := make([]string, 0, 8)
	for range 8 {
		repos = append(repos, testiso.Repo(t))
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(repos)*2)
	for i, r := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- RecordRepo(root, r)
			errs <- RecordCreatedPin(root, fmt.Sprintf("v0.0.%d", i))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := Repos(root)
	if err != nil || len(got) != 8 {
		t.Fatalf("Repos = %v, %v; want 8 lines", got, err)
	}
	pins, err := CreatedPins(root)
	if err != nil || len(pins) != 8 {
		t.Fatalf("CreatedPins = %v, %v; want 8 lines", pins, err)
	}
}

func TestLinkedWorktrees_ResolvesARelativeGitdirAgainstItsAdminDirectory(t *testing.T) {
	f := newFixture(t)
	tree := testiso.AddTree(t, f.repo, "feat-tree", "feat")
	admin := filepath.Join(f.repo, ".git", "worktrees", "feat-tree")
	rel, err := filepath.Rel(admin, filepath.Join(tree, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	testiso.Write(t, filepath.Join(admin, "gitdir"), filepath.ToSlash(rel)+"\n")

	got, _, err := LinkedWorktrees(f.repo)

	if want := []string{store.Canonical(tree)}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("LinkedWorktrees = %v, %v; want %v", got, err, want)
	}
}

// goneTree is a linked worktree whose directory was deleted without a git
// worktree prune, and the note line every verb prints for it.
func goneTree(t *testing.T, f fixture) (note string) {
	t.Helper()
	tree := testiso.AddTree(t, f.repo, "feat-tree", "feat")
	if err := os.RemoveAll(tree); err != nil {
		t.Fatal(err)
	}
	return "skip " + tree + ": not found, git worktree prune forgets it"
}

func TestInstall_AWorktreeThatIsGoneIsSkippedWithANoteAndTheUserLevelFileIsWritten(t *testing.T) {
	f := newFixture(t)
	note := goneTree(t, f)

	actions, err := Install(f.optsIn(false))
	if err != nil {
		t.Fatal(err)
	}

	if !contains(actions, note) {
		t.Errorf("no note %q in %q", note, actions)
	}
	if n := strings.Count(readFile(t, f.settings()), "/bin/v0.0.2/trellis' hook "); n != 6 {
		t.Errorf("%d trellis hooks in the user-level file, want 6", n)
	}
}

func TestEject_AWorktreeThatIsGoneIsSkippedWithANoteAndTheUserLevelFileIsCleaned(t *testing.T) {
	f := newFixture(t)
	if _, err := Install(f.optsIn(false)); err != nil {
		t.Fatal(err)
	}
	note := goneTree(t, f)

	actions, err := Eject(f.optsIn(false))
	if err != nil {
		t.Fatal(err)
	}

	if !contains(actions, note) {
		t.Errorf("no note %q in %q", note, actions)
	}
	if got := readFile(t, f.settings()); strings.Contains(got, "trellis") {
		t.Errorf("trellis entries left in the user-level file: %s", got)
	}
}

func TestPrune_AWorktreeThatIsGoneIsSkippedWithANote(t *testing.T) {
	f := newFixture(t)
	note := goneTree(t, f)

	actions, err := Prune(f.optsIn(false))

	if err != nil || !contains(actions, note) {
		t.Errorf("actions %q, err %v; want the note %q", actions, err, note)
	}
}

func TestRecordRepo_TimesOutOnTheFakeClockWhileAnotherInstallHoldsTheLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	holder, err := store.Open(filepath.Join(root, "records"), clock.Real())
	if err != nil {
		t.Fatal(err)
	}
	release, err := holder.Hold()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fc := clock.Fake(time.Unix(1000, 0))
	repo := testiso.Repo(t)
	done := make(chan error, 1)
	go func() { done <- RecordRepo(root, repo, WithClock(fc)) }()

	fc.BlockUntil(1) // waiting for the lock
	fc.Advance(recordLockTimeout)

	if err := <-done; !errors.Is(err, store.ErrLockTimeout) {
		t.Errorf("err = %v, want store.ErrLockTimeout", err)
	}
}

func TestInstall_InALinkedWorktreeRecordsThePrimaryAndMarksTheTree(t *testing.T) {
	f := newFixture(t)
	tree := testiso.AddTree(t, f.repo, "feat-tree", "feat")
	o := f.opts("v0.0.2", false)
	o.Repo = tree

	if _, err := Install(o); err != nil {
		t.Fatal(err)
	}

	got, err := Repos(f.root)
	if want := []string{store.Canonical(f.repo)}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Repos = %v, %v; want %v: the primary, not the tree", got, err, want)
	}
	if _, err := os.Stat(filepath.Join(tree, "trellis.toml")); err != nil {
		t.Errorf("trellis.toml not written in the tree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.repo, "trellis.toml")); !os.IsNotExist(err) {
		t.Errorf("trellis.toml written at the primary (%v)", err)
	}
}

func TestInstall_InATreeThatHoldsTheMarkerKeepsItWhileThePrimaryIsOff(t *testing.T) {
	f := newFixture(t)
	tree := testiso.AddTree(t, f.repo, "feat-tree", "feat")
	o := f.opts("v0.0.2", false)
	o.Repo = tree
	if _, err := Install(o); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, filepath.Join(tree, "trellis.toml"))

	actions, err := Install(o)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(actions, "keep trellis.toml") || slices.ContainsFunc(actions, func(a string) bool { return strings.HasPrefix(a, "write ") }) {
		t.Errorf("actions = %q, want keep trellis.toml and no write", actions)
	}
	if got := readFile(t, filepath.Join(tree, "trellis.toml")); got != before {
		t.Errorf("trellis.toml rewritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.repo, "trellis.toml")); !os.IsNotExist(err) {
		t.Errorf("trellis.toml appeared in the primary (%v)", err)
	}
}
