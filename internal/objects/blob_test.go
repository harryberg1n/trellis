package objects

import (
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/harryberg1n/trellis/internal/testsupport"

	"github.com/harryberg1n/trellis/internal/testiso"
)

// gitT runs git in dir and returns trimmed stdout.
func binary1MB() []byte {
	b := make([]byte, 1<<20)
	for i := range b {
		b[i] = byte(i*131 + i>>8)
	}
	return b
}

// inflated is the object file for id, decompressed.
func inflated(t testing.TB, gitDir, id string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join(gitDir, "objects", id[:2], id[2:]))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }() // read-only
	zr, err := zlib.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type blobCase struct {
	name    string
	content []byte
	wantSha string // the literal id for this content under the repo's format; "" = only compare with git
}

func checkBlobs(t *testing.T, repo string, cases []blobCase) {
	gitDir := filepath.Join(repo, ".git")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(repo, c.name)
			if err := os.WriteFile(p, c.content, 0o644); err != nil {
				t.Fatal(err)
			}
			id, err := WriteBlob(gitDir, p)
			if err != nil {
				t.Fatal(err)
			}
			if c.wantSha != "" && id != c.wantSha {
				t.Fatalf("id = %s, want %s", id, c.wantSha)
			}
			want := append([]byte("blob "+strconv.Itoa(len(c.content))+"\x00"), c.content...)
			ours := inflated(t, gitDir, id)
			if !bytes.Equal(ours, want) {
				t.Fatalf("object bytes differ from the literal header plus content")
			}
			// git must write the very same object; remove ours so git writes its own.
			if err := os.Remove(filepath.Join(gitDir, "objects", id[:2], id[2:])); err != nil {
				t.Fatal(err)
			}
			if gid := testsupport.Git(t, repo, "hash-object", "-w", c.name); gid != id {
				t.Fatalf("git hash-object id = %s, WriteBlob id = %s", gid, id)
			}
			if !bytes.Equal(inflated(t, gitDir, id), ours) {
				t.Fatal("object bytes differ from git's")
			}
			testsupport.Git(t, repo, "fsck", "--no-dangling")
		})
	}
}

func TestWriteBlob_MatchesGitHashObjectSha1(t *testing.T) {
	repo := testiso.Repo(t)
	checkBlobs(t, repo, []blobCase{
		{"hello.txt", []byte("hello\n"), "ce013625030ba8dba906f756967f9e9ca394464a"},
		{"empty", nil, "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"},
		{"big.bin", binary1MB(), ""},
	})
}

func TestWriteBlob_MatchesGitHashObjectSha256(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "r256")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	testsupport.Git(t, repo, "init", "-q", "--object-format=sha256")
	if f, err := Format(filepath.Join(repo, ".git")); err != nil || f != "sha256" {
		t.Fatalf("Format = %q, %v; want sha256", f, err)
	}
	checkBlobs(t, repo, []blobCase{
		{"hello.txt", []byte("hello\n"), ""},
		{"empty", nil, "473a0f4c3be8a93681a267e3b1e9a7dcda1185436fe141f7749120a303721813"},
		{"big.bin", binary1MB(), ""},
	})
}

func TestWriteBlob_SkipsExistingObject(t *testing.T) {
	repo := testiso.Repo(t)
	gitDir := filepath.Join(repo, ".git")
	p := filepath.Join(repo, "hello.txt")
	if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := WriteBlob(gitDir, p)
	if err != nil {
		t.Fatal(err)
	}
	obj := filepath.Join(gitDir, "objects", id[:2], id[2:])
	before, err := os.Stat(obj)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteBlob(gitDir, p); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(obj)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("the existing object was rewritten")
	}
	left, _ := filepath.Glob(filepath.Join(gitDir, "objects", id[:2], "tmp_*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestWriteBlob_NoChildProcess(t *testing.T) {
	repo := testiso.Repo(t)
	gitDir := filepath.Join(repo, ".git")
	p := filepath.Join(repo, "hello.txt")
	if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// WriteBlob takes no Runner, so it has no spawn seam to count. With PATH
	// empty, any attempt to start git would fail instead.
	t.Setenv("PATH", "")
	if _, err := exec.LookPath("git"); err == nil {
		t.Fatal("git is still found with an empty PATH; the test proves nothing")
	}
	id, err := WriteBlob(gitDir, p)
	if err != nil {
		t.Fatal(err)
	}
	if id != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Fatalf("id = %s", id)
	}
}
