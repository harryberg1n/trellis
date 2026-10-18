package objects

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/run"
)

// gitTimeout bounds each git child an anchor starts.
const gitTimeout = 30 * time.Second

const (
	modeFile = "100644"
	modeExec = "100755"
	modeLink = "120000"
	modeTree = "040000"
)

// RefFor is the replay ref of a lane: its branch name with every "/" turned
// into "--". The ref is under refs/trellis/, which no default refspec pushes.
func RefFor(lane string) string {
	return "refs/trellis/replay/" + strings.ReplaceAll(lane, "/", "--")
}

type entry struct{ mode, typ, id string }

type change struct {
	id   string // "" with del set
	del  bool
	mode string // "" = keep the old entry's mode, else a plain file
}

// AnchorTurn snapshots the working tree as a tree object and chains a commit
// for it onto the lane's replay ref, leaving the index, the branches and the
// working tree alone.
//
// The paths Bash changed come from git status; they are hashed with WriteBlob.
// blobs is path -> blob id for files already hashed (by the edit hook); a path
// status also names takes the file on disk. The new tree is HEAD's tree with
// those blobs replaced, built with mktree from the
// deepest directory up, one process per directory level. The commit's parent
// is the lane's previous anchor, read from the ref without a process.
//
// prevTree is accepted for the plan's signature and ignored: status is relative
// to HEAD, so a base other than HEAD's tree would keep a blob that status no
// longer names (a file reverted to HEAD, or HEAD moving between turns). Every
// turn is HEAD plus the full status set.
//
// Git kinds used: status, ls-tree, mktree, commit-tree, update-ref; rev-parse
// only when the replay ref is not a plain ref file (symbolic, reftable).
func AnchorTurn(r run.Runner, repo, lane, _ string, blobs map[string]string) (tree, commit string, err error) {
	common, gitDir, err := GitDirs(repo)
	if err != nil {
		return "", "", err
	}
	filemode := coreFilemode(gitDir)
	changes := map[string]change{}
	for p, id := range blobs {
		changes[path.Clean(filepath.ToSlash(p))] = change{id: id}
	}
	st, err := Git(r, repo, nil, "status", "-z", "--untracked-files=all")
	if err != nil {
		return "", "", err
	}
	for _, p := range statusPaths(st) {
		full := filepath.Join(repo, filepath.FromSlash(p))
		info, err := os.Lstat(full)
		switch {
		case pathGone(err):
			changes[p] = change{del: true}
		case err != nil:
			return "", "", err
		case info.IsDir():
			// A nested repository: not a blob this anchor makes.
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(full)
			if err != nil {
				return "", "", err
			}
			id, err := writeBlobContent(gitDir, []byte(target))
			if err != nil {
				return "", "", err
			}
			changes[p] = change{id: id, mode: modeLink}
		default:
			id, err := WriteBlob(gitDir, full)
			if err != nil {
				return "", "", err
			}
			mode := "" // filemode off: the entry already in the tree keeps its mode
			if filemode {
				mode = modeFile
				if info.Mode()&0o111 != 0 {
					mode = modeExec
				}
			}
			changes[p] = change{id: id, mode: mode}
		}
	}

	root := "HEAD"
	if headUnborn(common, gitDir) {
		root = "" // no commit yet: start from the empty tree
	}
	if tree, err = buildTree(r, repo, root, changes); err != nil {
		return "", "", err
	}

	ref := RefFor(lane)
	prev, hasPrev, viaGit := readRef(common, ref)
	if viaGit {
		if prev, hasPrev, err = resolveRef(r, repo, ref); err != nil {
			return "", "", err
		}
	}
	args := []string{"-c", "user.name=trellis", "-c", "user.email=trellis@localhost", "-c", "commit.gpgsign=false", "commit-tree", tree}
	if hasPrev {
		args = append(args, "-p", prev)
	}
	args = append(args, "-m", "trellis anchor "+lane)
	out, err := Git(r, repo, nil, args...)
	if err != nil {
		return "", "", err
	}
	commit = strings.TrimSpace(string(out))
	upd := []string{"update-ref", "--no-deref", ref, commit}
	if hasPrev && !viaGit {
		upd = append(upd, prev)
	}
	if _, err := Git(r, repo, nil, upd...); err != nil {
		return "", "", err
	}
	return tree, commit, nil
}

// buildTree applies changes to the tree named by root (a tree id or HEAD) and
// returns the new root tree's id.
func buildTree(r run.Runner, repo, root string, changes map[string]change) (string, error) {
	dirs := map[string]map[string]entry{"": nil}
	for p := range changes {
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			dirs[d] = nil
		}
	}
	order := make([]string, 0, len(dirs))
	for d := range dirs {
		order = append(order, d)
	}
	sort.Slice(order, func(i, j int) bool {
		if di, dj := depth(order[i]), depth(order[j]); di != dj {
			return di < dj
		}
		return order[i] < order[j]
	})
	// Load top down, so a directory is listed only if its parent has it.
	for _, d := range order {
		if root == "" {
			dirs[d] = map[string]entry{}
			continue
		}
		spec := root
		if d != "" {
			parent := dirs[parentOf(d)]
			if e, ok := parent[path.Base(d)]; !ok || e.typ != "tree" {
				dirs[d] = map[string]entry{}
				continue
			}
			spec = root + ":" + d
		}
		out, err := Git(r, repo, nil, "ls-tree", "-z", spec)
		if err != nil {
			return "", err
		}
		list, err := parseLsTree(out)
		if err != nil {
			return "", err
		}
		dirs[d] = list
	}
	for p, c := range changes {
		list := dirs[parentOf(p)]
		name := path.Base(p)
		switch {
		case c.del:
			delete(list, name)
		default:
			mode := c.mode
			if mode == "" { // named in blobs alone: the old mode stands
				mode = modeFile
				if old, ok := list[name]; ok && old.typ == "blob" {
					mode = old.mode
				}
			}
			list[name] = entry{mode, "blob", c.id}
		}
	}
	// Bottom up: one mktree process per level, every directory of the level in
	// it; an emptied directory leaves its parent.
	for i := len(order) - 1; i >= 0; {
		lvl := depth(order[i])
		j := i
		for j >= 0 && depth(order[j]) == lvl {
			j--
		}
		level := order[j+1 : i+1]
		i = j
		var batch []string
		var in bytes.Buffer
		for _, d := range level {
			list := dirs[d]
			if len(list) == 0 && d != "" {
				// An emptied directory leaves its parent, unless the name is a
				// file now (a directory replaced by a file).
				if e, ok := dirs[parentOf(d)][path.Base(d)]; ok && e.typ == "tree" {
					delete(dirs[parentOf(d)], path.Base(d))
				}
				continue
			}
			if len(batch) > 0 {
				in.WriteByte(0) // an empty record ends a tree
			}
			names := make([]string, 0, len(list))
			for n := range list {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				e := list[n]
				fmt.Fprintf(&in, "%s %s %s\t%s\x00", e.mode, e.typ, e.id, n)
			}
			batch = append(batch, d)
		}
		if len(batch) == 0 {
			continue
		}
		args := []string{"mktree", "-z", "--missing"}
		if len(batch) > 1 {
			args = append(args, "--batch")
		}
		out, err := Git(r, repo, append([]byte{}, in.Bytes()...), args...)
		if err != nil {
			return "", err
		}
		ids := strings.Fields(string(out))
		if len(ids) != len(batch) {
			return "", fmt.Errorf("objects: mktree answered %d ids for %d trees", len(ids), len(batch))
		}
		for k, d := range batch {
			if d == "" {
				return ids[k], nil
			}
			dirs[parentOf(d)][path.Base(d)] = entry{modeTree, "tree", ids[k]}
		}
	}
	return "", errors.New("objects: the root tree was never built")
}

func depth(d string) int {
	if d == "" {
		return 0
	}
	return strings.Count(d, "/") + 1
}

// parentOf is the directory holding p, "" for the root.
func parentOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// parseLsTree reads `git ls-tree -z` output: "mode type id\tname\0".
func parseLsTree(out []byte) (map[string]entry, error) {
	list := map[string]entry{}
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, name, ok := strings.Cut(string(rec), "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("objects: unreadable ls-tree record %q", rec)
		}
		list[name] = entry{f[0], f[1], f[2]}
	}
	return list, nil
}

// Git runs one git child through r and returns its stdout.
func Git(r run.Runner, repo string, stdin []byte, args ...string) ([]byte, error) {
	res, err := r.Light(run.Spec{Name: "git", Args: args, Dir: repo, Timeout: gitTimeout, Stdin: stdin})
	if err != nil {
		return nil, fmt.Errorf("objects: git %s: %w", run.Kind(run.Spec{Name: "git", Args: args}), err)
	}
	return res.Stdout, nil
}

// GitDirs is the repo's own git dir and the common one that holds objects and
// refs (the same, except in a linked worktree).
func GitDirs(repo string) (common, gitDir string, err error) {
	dot := filepath.Join(repo, ".git")
	info, err := os.Stat(dot)
	if err != nil {
		return "", "", err
	}
	gitDir = dot
	if !info.IsDir() {
		raw, err := os.ReadFile(dot)
		if err != nil {
			return "", "", err
		}
		p, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
		if !ok {
			return "", "", fmt.Errorf("objects: %s is not a gitdir file", dot)
		}
		gitDir = strings.TrimSpace(p)
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(repo, gitDir)
		}
	}
	return commonDir(gitDir), gitDir, nil
}

// readRef reads a ref from its loose file, else from packed-refs, without
// starting git. fallback is set when the files cannot answer: the ref file
// cannot be read for a reason other than not existing, it is a symbolic ref, or
// the repo keeps its refs in reftable. The caller then asks git.
func readRef(common, ref string) (id string, found, fallback bool) {
	if _, err := os.Stat(filepath.Join(common, "reftable")); err == nil {
		return "", false, true
	}
	raw, err := os.ReadFile(filepath.Join(common, filepath.FromSlash(ref)))
	switch {
	case err == nil:
		s := strings.TrimSpace(string(raw))
		if s == "" || strings.HasPrefix(s, "ref:") {
			return "", false, true
		}
		return s, true, false
	case !errors.Is(err, fs.ErrNotExist):
		return "", false, true
	}
	f, err := os.Open(filepath.Join(common, "packed-refs"))
	if err != nil {
		return "", false, !errors.Is(err, fs.ErrNotExist)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		id, name, ok := strings.Cut(sc.Text(), " ")
		if ok && name == ref && !strings.HasPrefix(id, "#") {
			return id, true, false
		}
	}
	return "", false, false
}

// headUnborn reports whether HEAD names a branch with no commit yet, judged
// from the files. A reftable repo keeps its refs elsewhere, so it is taken as
// born and git is left to say otherwise.
func headUnborn(common, gitDir string) bool {
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return false
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "ref:")
	if !ok {
		return false
	}
	if _, err := os.Stat(filepath.Join(common, "reftable")); err == nil {
		return false
	}
	_, found, fallback := readRef(common, strings.TrimSpace(target))
	return !found && !fallback
}

// resolveRef asks git for a ref the files could not answer. It runs only then
// (a symbolic ref, reftable), so the usual turn spawns no rev-parse. A ref that
// does not exist answers exit 1 and is no parent.
func resolveRef(r run.Runner, repo, ref string) (string, bool, error) {
	res, err := r.Light(run.Spec{Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", ref}, Dir: repo, Timeout: gitTimeout})
	if err != nil {
		if res.ExitCode == 1 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("objects: git rev-parse %s: %w", ref, err)
	}
	return strings.TrimSpace(string(res.Stdout)), true, nil
}
