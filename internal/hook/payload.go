// Package hook turns agent-harness hook payloads into trellis events and
// records them. A hook is silent: it prints only what the walls return, and
// in this package that is nothing. Whatever goes wrong becomes an
// internal.error event and the hook still exits 0, so the recorder can never
// block the agent.
package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/harryberg1n/trellis/internal/objects"
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

// ReadPayload reads one payload from r, up to the size bound.
func ReadPayload(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxPayload))
	if err != nil {
		return nil, fmt.Errorf("reading stdin: %w", err)
	}
	return raw, nil
}

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
	Lane   string // the branch HEAD names, or "detached"
	Remote string // the URL of remote origin, "" if it has none
}

// Locate finds the repository around cwd. The reading is objects' (FindRoot,
// GitDirs, Branch, ConfigValue); nothing is spawned.
func Locate(cwd string) (Repo, error) {
	root, err := objects.FindRoot(cwd)
	if err != nil {
		return Repo{}, err
	}
	_, gitDir, err := objects.GitDirs(root)
	if err != nil {
		return Repo{}, fmt.Errorf("git dirs of %s: %w", root, err)
	}
	lane, err := objects.Branch(gitDir)
	if err != nil {
		return Repo{}, fmt.Errorf("branch of %s: %w", root, err)
	}
	remote, _, err := objects.ConfigValue(gitDir, `remote "origin"`, "url")
	if err != nil {
		return Repo{}, fmt.Errorf("remote of %s: %w", root, err)
	}
	return Repo{Root: root, GitDir: gitDir, Lane: lane, Remote: remote}, nil
}

// PayloadCwd is the cwd a payload names, "" if it names none or is not JSON.
// The command uses it to pick the repo before the store is opened.
func PayloadCwd(raw []byte) string {
	p, _ := decode(raw)
	return p.Cwd
}
