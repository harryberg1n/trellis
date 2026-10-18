package objects

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/testsupport"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/testiso"
)

const (
	idHello  = "ce013625030ba8dba906f756967f9e9ca394464a" // "hello\n"
	idA      = "78981922613b2afb6025042ff6bd878ac1994e85" // "a\n"
	idEmpty  = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"
	idTarget = "4cbb553f3f4ac2ee7b01ff6c951d6bf583c39c15" // "target.txt", a symlink target
)

func TestRefFor(t *testing.T) {
	cases := map[string]string{
		"main":          "refs/trellis/replay/main",
		"feature/x":     "refs/trellis/replay/feature--x",
		"a/b/c":         "refs/trellis/replay/a--b--c",
		"already--done": "refs/trellis/replay/already--done",
	}
	for lane, want := range cases {
		if got := RefFor(lane); got != want {
			t.Errorf("RefFor(%q) = %q, want %q", lane, got, want)
		}
	}
}

func TestAnchorTurn_SurvivesGcPrune(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Write(t, filepath.Join(repo, "one.txt"), "hello\n")
	testiso.Write(t, filepath.Join(repo, "dir", "two.txt"), "a\n")
	// one.txt was hashed by the edit hook; two.txt is found by status.
	id1, err := WriteBlob(filepath.Join(repo, ".git"), filepath.Join(repo, "one.txt"))
	if err != nil {
		t.Fatal(err)
	}
	tree, commit, err := AnchorTurn(testsupport.Recorder(), repo, "feature/gc", "", map[string]string{"one.txt": id1})
	if err != nil {
		t.Fatal(err)
	}
	testiso.Git(t, repo, "gc", "--prune=now")
	for _, id := range []string{idHello, idA, tree, commit} {
		testiso.Git(t, repo, "cat-file", "-e", id)
	}
}

func TestAnchorTurn_BashEditsCaptured(t *testing.T) {
	repo := testiso.Repo(t)
	// Written the way Bash would: no hook saw them.
	testiso.Write(t, filepath.Join(repo, "new.txt"), "hello\n")
	testiso.Write(t, filepath.Join(repo, "deep", "er", "still.txt"), "a\n")
	testiso.Write(t, filepath.Join(repo, "README.md"), "") // a tracked file, emptied
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := testiso.Git(t, repo, "ls-tree", "-r", commit)
	want := strings.Join([]string{
		"100644 blob " + idEmpty + "\tREADME.md",
		"100644 blob " + idA + "\tdeep/er/still.txt",
		"100644 blob " + idHello + "\tnew.txt",
	}, "\n")
	if got != want {
		t.Fatalf("ls-tree -r:\n%s\nwant:\n%s", got, want)
	}
}

func TestAnchorTurn_DeletedFileLeavesTree(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Write(t, filepath.Join(repo, "sub", "keep.txt"), "a\n")
	testiso.Write(t, filepath.Join(repo, "sub", "gone.txt"), "hello\n")
	testiso.Git(t, repo, "add", ".")
	testiso.Git(t, repo, "commit", "-qm", "two files")
	if err := os.Remove(filepath.Join(repo, "sub", "gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "README.md")); err != nil {
		t.Fatal(err)
	}
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := testiso.Git(t, repo, "ls-tree", "-r", "--name-only", commit)
	if got != "sub/keep.txt" {
		t.Fatalf("tree lists %q, want only sub/keep.txt", got)
	}
}

func TestAnchorTurn_ChainsPreviousAnchor(t *testing.T) {
	repo := testiso.Repo(t)
	r := testsupport.Recorder()
	testiso.Write(t, filepath.Join(repo, "a.txt"), "a\n")
	tree1, c1, err := AnchorTurn(r, repo, "feature/chain", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := testiso.Git(t, repo, "rev-parse", "refs/trellis/replay/feature--chain"); got != c1 {
		t.Fatalf("ref = %s, want %s", got, c1)
	}
	testiso.Write(t, filepath.Join(repo, "b.txt"), "hello\n")
	_, c2, err := AnchorTurn(r, repo, "feature/chain", tree1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := testiso.Git(t, repo, "rev-parse", c2+"^"); got != c1 {
		t.Fatalf("parent of the second anchor = %s, want %s", got, c1)
	}
	if got := testiso.Git(t, repo, "rev-parse", "refs/trellis/replay/feature--chain"); got != c2 {
		t.Fatalf("ref = %s, want %s", got, c2)
	}
	if got := testiso.Git(t, repo, "ls-tree", "-r", "--name-only", c2); got != "README.md\na.txt\nb.txt" {
		t.Fatalf("second anchor lists %q", got)
	}
	// The user's branch and index are untouched.
	if got := testiso.Git(t, repo, "status", "--porcelain"); got != "?? a.txt\n?? b.txt" {
		t.Fatalf("status = %q", got)
	}
}

func TestAnchorTurn_RefIsNeverPushed(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Write(t, filepath.Join(repo, "a.txt"), "a\n")
	if _, _, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(t.TempDir(), "remote.git")
	testiso.Git(t, repo, "init", "-q", "--bare", bare)
	testiso.Git(t, repo, "remote", "add", "origin", bare)
	testiso.Git(t, repo, "push", "-q", "-u", "origin", "main")
	got := testiso.Git(t, repo, "ls-remote", "origin")
	if !strings.Contains(got, "refs/heads/main") {
		t.Fatalf("the push did not happen: %q", got)
	}
	if strings.Contains(got, "refs/trellis/") {
		t.Fatalf("ls-remote shows a replay ref:\n%s", got)
	}
}

func TestAnchorTurn_GitInvocationKinds(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Write(t, filepath.Join(repo, "a", "b", "c.txt"), "a\n")
	testiso.Write(t, filepath.Join(repo, "d.txt"), "hello\n")
	r := testsupport.Recorder()
	if _, _, err := AnchorTurn(r, repo, "main", "", nil); err != nil {
		t.Fatal(err)
	}
	// Five kinds, one over the plan's four: ls-tree lists the unchanged
	// siblings of each changed directory, which mktree needs and git status
	// does not give.
	want := map[string]bool{"status": true, "ls-tree": true, "mktree": true, "commit-tree": true, "update-ref": true}
	got := map[string]bool{}
	for _, k := range r.Kinds() {
		got[k] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", r.Kinds(), want)
	}
	kinds := r.Kinds()
	if kinds[0] != "status" || kinds[len(kinds)-1] != "update-ref" {
		t.Fatalf("order = %v, want status first and update-ref last", kinds)
	}
	for _, s := range r.Specs() {
		if s.Name != "git" {
			t.Fatalf("spawned %q, want only git", s.Name)
		}
		if s.Timeout <= 0 {
			t.Fatalf("spec %v has no timeout", s.Args)
		}
	}
}

func TestCorpus_NoContentInRecords(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Write(t, filepath.Join(repo, "secret.txt"), "SENTINEL-7f3a\n")
	tree, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	s, err := store.Open(state, clock.Fake(time.Unix(1000, 0)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Append(kernel.Event{
		Kind: kernel.EvTurnAnchored, Lane: "main", Session: "s1", At: time.Unix(1000, 0),
		Detail: map[string]string{"tree": tree, "commit": commit, "paths": "secret.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	files, hits := 0, 0
	err = filepath.WalkDir(state, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		raw, err := os.ReadFile(p)
		hits += strings.Count(string(raw), "SENTINEL-7f3a")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no record file was written")
	}
	if hits != 0 {
		t.Fatalf("the sentinel appears %d times in the record files", hits)
	}
}

const idTemplate = "3236cba6d6ebe89a13ca485fe17c890548d23da5" // "template\n", README.md at HEAD

func TestAnchorTurn_RevertToHeadDropsTheStaleBlob(t *testing.T) {
	repo := testiso.Repo(t)
	r := testsupport.Recorder()
	testiso.Write(t, filepath.Join(repo, "README.md"), "hello\n")
	tree1, _, err := AnchorTurn(r, repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := testiso.Git(t, repo, "ls-tree", tree1, "README.md"); got != "100644 blob "+idHello+"\tREADME.md" {
		t.Fatalf("turn 1: %q", got)
	}
	testiso.Write(t, filepath.Join(repo, "README.md"), "template\n") // back to HEAD's content
	tree2, _, err := AnchorTurn(r, repo, "main", tree1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := testiso.Git(t, repo, "ls-tree", tree2, "README.md"); got != "100644 blob "+idTemplate+"\tREADME.md" {
		t.Fatalf("turn 2 kept the stale blob: %q", got)
	}
}

func TestAnchorTurn_FollowsHeadAfterCommit(t *testing.T) {
	repo := testiso.Repo(t)
	r := testsupport.Recorder()
	testiso.Write(t, filepath.Join(repo, "one.txt"), "hello\n")
	tree1, _, err := AnchorTurn(r, repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	testiso.Git(t, repo, "add", "one.txt")
	testiso.Git(t, repo, "commit", "-qm", "one")
	testiso.Write(t, filepath.Join(repo, "two.txt"), "a\n")
	_, commit, err := AnchorTurn(r, repo, "main", tree1, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "README.md\none.txt\ntwo.txt"
	if got := testiso.Git(t, repo, "ls-tree", "-r", "--name-only", commit); got != want {
		t.Fatalf("tree lists %q, want %q", got, want)
	}
	if got := testiso.Git(t, repo, "ls-tree", commit, "one.txt"); got != "100644 blob "+idHello+"\tone.txt" {
		t.Fatalf("one.txt: %q", got)
	}
}

func TestAnchorTurn_UnbornHeadStartsFromEmptyTree(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "fresh")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	testiso.Git(t, repo, "init", "-q")
	testiso.Write(t, filepath.Join(repo, "only", "file.txt"), "hello\n")
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "100644 blob " + idHello + "\tonly/file.txt"
	if got := testiso.Git(t, repo, "ls-tree", "-r", commit); got != want {
		t.Fatalf("tree lists %q, want %q", got, want)
	}
}

func TestAnchorTurn_ExecBitIsRecorded(t *testing.T) {
	skipNoExecBit(t)
	repo := testiso.Repo(t)
	if err := os.Chmod(filepath.Join(repo, "README.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := testiso.Git(t, repo, "ls-tree", commit, "README.md"); got != "100755 blob "+idTemplate+"\tREADME.md" {
		t.Fatalf("README.md: %q", got)
	}
}

func TestAnchorTurn_SymlinkIsRecordedAs120000(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Git(t, repo, "config", "core.symlinks", "true")
	if err := os.Symlink("target.txt", filepath.Join(repo, "link")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := testiso.Git(t, repo, "ls-tree", commit, "link"); got != "120000 blob "+idTarget+"\tlink" {
		t.Fatalf("link: %q", got)
	}
}

func TestAnchorTurn_SymlinkReplacedByFileIsRegular(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Git(t, repo, "config", "core.filemode", "true") // a replacing file is taken as it is on disk
	testiso.Git(t, repo, "config", "core.symlinks", "true")
	if err := os.Symlink("target.txt", filepath.Join(repo, "link")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	testiso.Git(t, repo, "add", "link")
	testiso.Git(t, repo, "commit", "-qm", "link")
	if err := os.Remove(filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}
	testiso.Write(t, filepath.Join(repo, "link"), "hello\n")
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := testiso.Git(t, repo, "ls-tree", commit, "link"); got != "100644 blob "+idHello+"\tlink" {
		t.Fatalf("link: %q", got)
	}
}

func TestAnchorTurn_DirectoryReplacedByFile(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Write(t, filepath.Join(repo, "d", "x.txt"), "hello\n")
	testiso.Git(t, repo, "add", "d")
	testiso.Git(t, repo, "commit", "-qm", "dir")
	if err := os.RemoveAll(filepath.Join(repo, "d")); err != nil {
		t.Fatal(err)
	}
	testiso.Write(t, filepath.Join(repo, "d"), "a\n")
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "100644 blob " + idTemplate + "\tREADME.md\n100644 blob " + idA + "\td"
	if got := testiso.Git(t, repo, "ls-tree", commit); got != want {
		t.Fatalf("root:\n%s\nwant:\n%s", got, want)
	}
}

func TestAnchorTurn_Sha256Repo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "r256")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	testiso.Git(t, repo, "init", "-q", "--object-format=sha256")
	testiso.Write(t, filepath.Join(repo, "top.txt"), "hello\n")
	testiso.Write(t, filepath.Join(repo, "sub", "deep.txt"), "a\n")
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(testiso.Git(t, repo, "ls-tree", "-r", commit), "\n")
	if len(lines) != 2 {
		t.Fatalf("tree lists %q", lines)
	}
	for _, l := range lines {
		meta, name, _ := strings.Cut(l, "\t")
		f := strings.Fields(meta)
		if len(f) != 3 || len(f[2]) != 64 {
			t.Fatalf("%q: id is not 64 hex chars", l)
		}
		if want := testiso.Git(t, repo, "hash-object", name); f[2] != want {
			t.Fatalf("%s: tree id %s, git hash-object %s", name, f[2], want)
		}
	}
	if lines[0][strings.Index(lines[0], "\t")+1:] != "sub/deep.txt" {
		t.Fatalf("unexpected order or paths: %q", lines)
	}
}

func TestAnchorTurn_RenameAndSpaces(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Write(t, filepath.Join(repo, "a.txt"), "hello\n")
	testiso.Git(t, repo, "add", "a.txt")
	testiso.Git(t, repo, "commit", "-qm", "a")
	if err := os.MkdirAll(filepath.Join(repo, "dir with space"), 0o755); err != nil {
		t.Fatal(err)
	}
	testiso.Git(t, repo, "mv", "a.txt", "dir with space/b c.txt")
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "100644 blob " + idTemplate + "\tREADME.md\n100644 blob " + idHello + "\tdir with space/b c.txt"
	if got := testiso.Git(t, repo, "ls-tree", "-r", commit); got != want {
		t.Fatalf("tree:\n%s\nwant:\n%s", got, want)
	}
}

func TestAnchorTurn_SymbolicReplayRefFallsBackToGit(t *testing.T) {
	repo := testiso.Repo(t)
	r := testsupport.Recorder()
	testiso.Write(t, filepath.Join(repo, "a.txt"), "a\n")
	_, c0, err := AnchorTurn(r, repo, "other", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// main's replay ref is a symbolic ref to other's: not a plain ref file.
	testiso.Git(t, repo, "symbolic-ref", "refs/trellis/replay/main", "refs/trellis/replay/other")
	r2 := testsupport.Recorder()
	_, c1, err := AnchorTurn(r2, repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := testiso.Git(t, repo, "rev-parse", c1+"^"); got != c0 {
		t.Fatalf("parent = %s, want the commit the symbolic ref resolves to, %s", got, c0)
	}
	if k := r2.Kinds(); !contains(k, "rev-parse") {
		t.Fatalf("kinds = %v, want a rev-parse fallback", k)
	}
	// Other's ref must not have moved through the symref.
	if got := testiso.Git(t, repo, "rev-parse", "refs/trellis/replay/other"); got != c0 {
		t.Fatalf("other moved to %s", got)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestAnchorTurn_NoFilemodeKeepsExistingMode(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Git(t, repo, "config", "core.filemode", "false")
	testiso.Write(t, filepath.Join(repo, "run.sh"), "a\n")
	testiso.Git(t, repo, "add", "run.sh")
	testiso.Git(t, repo, "update-index", "--chmod=+x", "run.sh")
	testiso.CommitAll(t, repo, "script")
	if got := testiso.Git(t, repo, "ls-tree", "HEAD", "run.sh"); got != "100755 blob "+idA+"\trun.sh" {
		t.Fatalf("setup: %q", got)
	}
	testiso.Write(t, filepath.Join(repo, "run.sh"), "hello\n")
	testiso.Write(t, filepath.Join(repo, "fresh.sh"), "a\n")
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := testiso.Git(t, repo, "ls-tree", commit, "run.sh"); got != "100755 blob "+idHello+"\trun.sh" {
		t.Fatalf("run.sh: %q", got)
	}
	if got := testiso.Git(t, repo, "ls-tree", commit, "fresh.sh"); got != "100644 blob "+idA+"\tfresh.sh" {
		t.Fatalf("a new file must be 100644: %q", got)
	}
}

func TestAnchorTurn_NoFilemodeKeepsSymlinkEntryAsCheckedOutFile(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Git(t, repo, "config", "core.filemode", "false")
	testiso.Git(t, repo, "config", "core.symlinks", "false") // model a Windows checkout on every OS
	testiso.Git(t, repo, "update-index", "--add", "--cacheinfo", "120000,"+idTarget+",link")
	testiso.Write(t, filepath.Join(repo, "link"), "target.txt") // a core.symlinks=false checkout
	testiso.CommitAll(t, repo, "link")
	testiso.Write(t, filepath.Join(repo, "link"), "hello\n")
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := testiso.Git(t, repo, "ls-tree", commit, "link"); got != "120000 blob "+idHello+"\tlink" {
		t.Fatalf("link: %q", got)
	}
}

func TestAnchorTurn_FilemodeTrueUsesLstat(t *testing.T) {
	skipNoExecBit(t)
	repo := testiso.Repo(t)
	testiso.Git(t, repo, "config", "core.filemode", "true")
	testiso.Write(t, filepath.Join(repo, "run.sh"), "a\n")
	if err := os.Chmod(filepath.Join(repo, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	testiso.Git(t, repo, "add", "run.sh")
	testiso.CommitAll(t, repo, "script")
	if err := os.Chmod(filepath.Join(repo, "run.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, commit, err := AnchorTurn(testsupport.Recorder(), repo, "main", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := testiso.Git(t, repo, "ls-tree", commit, "run.sh"); got != "100644 blob "+idA+"\trun.sh" {
		t.Fatalf("run.sh: %q", got)
	}
}
