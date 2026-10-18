// Package hook turns agent-harness hook payloads into trellis events and
// records them. A hook is silent: it prints only what the walls return, and
// in this package that is nothing. Whatever goes wrong becomes an
// internal.error event and the hook still exits 0, so the recorder can never
// block the agent.
package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// payload is the part of a harness payload the recorder reads. Every hook
// gets the common fields; the rest depend on the event. Unknown fields are
// ignored, so a newer harness does not break the recorder.
type payload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	ToolName       string `json:"tool_name"`
	ToolInput      struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Command      string `json:"command"`
	} `json:"tool_input"`
	Reason string `json:"reason"` // SessionEnd
}

// maxPayload bounds what a hook reads from stdin. A Write of a large file is
// the biggest payload there is.
const maxPayload = 64 << 20

func decode(raw []byte) (payload, error) {
	var p payload
	if len(bytes.TrimSpace(raw)) == 0 {
		return p, errors.New("hook: empty payload")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("hook: decoding payload: %w", err)
	}
	return p, nil
}

// Repo is the git repository a payload's cwd lies in.
type Repo struct {
	Root   string // the working tree
	GitDir string // its own git dir (the worktree's, in a linked worktree)
	Common string // the git dir that holds objects, refs and config
	Lane   string // the branch HEAD names, or "detached"
	Remote string // the URL of remote origin, "" if it has none
}

// ErrNotARepo is what Locate returns when no .git lies at or above cwd.
var ErrNotARepo = errors.New("hook: not in a git repository")

// Locate finds the repository around cwd by walking up to a .git entry and
// reading HEAD, the worktree's commondir and the config. It starts no process.
func Locate(cwd string) (Repo, error) {
	if !filepath.IsAbs(cwd) {
		return Repo{}, fmt.Errorf("hook: cwd %q is not absolute", cwd)
	}
	dir := filepath.Clean(cwd)
	for {
		dot := filepath.Join(dir, ".git")
		if info, err := os.Stat(dot); err == nil {
			return readRepo(dir, dot, info.IsDir())
		}
		up := filepath.Dir(dir)
		if up == dir {
			return Repo{}, fmt.Errorf("%w: %s", ErrNotARepo, cwd)
		}
		dir = up
	}
}

func readRepo(root, dot string, isDir bool) (Repo, error) {
	r := Repo{Root: root, GitDir: dot}
	if !isDir {
		raw, err := os.ReadFile(dot)
		if err != nil {
			return Repo{}, err
		}
		p, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
		if !ok {
			return Repo{}, fmt.Errorf("hook: %s is not a gitdir file", dot)
		}
		r.GitDir = strings.TrimSpace(p)
		if !filepath.IsAbs(r.GitDir) {
			r.GitDir = filepath.Join(root, r.GitDir)
		}
	}
	r.Common = r.GitDir
	if raw, err := os.ReadFile(filepath.Join(r.GitDir, "commondir")); err == nil {
		if p := strings.TrimSpace(string(raw)); p != "" {
			if !filepath.IsAbs(p) {
				p = filepath.Join(r.GitDir, p)
			}
			r.Common = filepath.Clean(p)
		}
	}
	head, err := os.ReadFile(filepath.Join(r.GitDir, "HEAD"))
	if err != nil {
		return Repo{}, err
	}
	r.Lane = laneOf(string(head))
	r.Remote = originURL(filepath.Join(r.Common, "config"))
	return r, nil
}

// laneOf is the branch a HEAD file names. A HEAD that holds a commit id is a
// detached checkout.
func laneOf(head string) string {
	ref, ok := strings.CutPrefix(strings.TrimSpace(head), "ref:")
	if !ok {
		return "detached"
	}
	ref = strings.TrimSpace(ref)
	return strings.TrimPrefix(ref, "refs/heads/")
}

// originURL is remote.origin.url from a git config file, "" if there is none.
func originURL(config string) string {
	f, err := os.Open(config)
	if err != nil {
		return ""
	}
	defer f.Close()
	in, url := false, ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			in = strings.EqualFold(strings.Join(strings.Fields(line), " "), `[remote "origin"]`)
			continue
		}
		if k, v, ok := strings.Cut(line, "="); in && ok && strings.EqualFold(strings.TrimSpace(k), "url") {
			url = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return url
}

// PayloadCwd is the cwd a payload names, "" if it names none or is not JSON.
// The command uses it to pick the repo before the store is opened.
func PayloadCwd(raw []byte) string {
	p, _ := decode(raw)
	return p.Cwd
}
