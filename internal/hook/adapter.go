package hook

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/store"
)

// canonical maps a hook event as written on the command line ("PostToolUse",
// "post-tool-use", "posttooluse") to the harness's name for it. Besides the
// events the recorder records, it accepts the harness events that carry
// nothing to record yet, so wiring one up is not an error.
func canonical(event string) (string, bool) {
	switch strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(event)) {
	case "pretooluse":
		return kernel.HookPreToolUse, true
	case "posttooluse":
		return kernel.HookPostToolUse, true
	case "stop":
		return kernel.HookStop, true
	case "sessionstart":
		return kernel.HookSessionStart, true
	case "sessionend":
		return kernel.HookSessionEnd, true
	case "userpromptsubmit":
		return kernel.HookUserPromptSubmit, true
	case "posttoolusefailure":
		return kernel.HookPostToolUseFailure, true
	case "subagentstart":
		return kernel.HookSubagentStart, true
	case "subagentstop":
		return kernel.HookSubagentStop, true
	case "notification":
		return kernel.HookNotification, true
	case "precompact":
		return kernel.HookPreCompact, true
	}
	return "", false
}

// isFileTool reports whether tool's tool_input names the file it changes.
func isFileTool(tool string) bool {
	switch tool {
	case kernel.ToolEdit, kernel.ToolWrite, kernel.ToolMultiEdit, kernel.ToolNotebookEdit:
		return true
	}
	return false
}

// parsed is a payload read as far as the effects need it.
type parsed struct {
	ev   kernel.Event
	ctx  kernel.Context
	repo objects.Repo
	file string // absolute path of the file an Edit or Write changed
}

// Parse reads one payload into the event to record and the context the rules
// match on. The lane is the repo's current branch, read from HEAD without a
// process; the session is the payload's session_id. ev.At is left for the
// caller, and ev.Detail holds only what the payload says: an edit's blob and a
// turn's tree and commit come from the effects Run performs. An event with
// Kind "" has nothing to record (PreToolUse, SessionStart, UserPromptSubmit);
// its context is still filled for the walls.
//
// The context carries Tool (the tool name) and Args: cwd, repo (the working
// tree root), file_path and path (the changed file, absolute and relative to
// the repo with forward slashes), command (Bash and PowerShell) and person
// ("1" for a PreToolUse payload with no session_id).
func Parse(event string, raw []byte) (kernel.Event, kernel.Context, error) {
	p, err := parse(event, raw, nil)
	return p.ev, p.ctx, err
}

// parse is Parse with the repo the caller already located, or nil to locate it.
func parse(event string, raw []byte, known *objects.Repo) (parsed, error) {
	var out parsed
	name, ok := canonical(event)
	if !ok {
		return out, fmt.Errorf("hook: unknown hook event %q", event)
	}
	p, err := decode(raw)
	if err != nil {
		return out, err
	}
	out.ev.Session = p.SessionID
	// A PreToolUse with no session_id is a person at a git hook, not an agent:
	// the walls warn them where they would deny an agent.
	person := p.SessionID == "" && name == kernel.HookPreToolUse
	if p.SessionID == "" && !person {
		return out, fmt.Errorf("hook: %s payload has no session_id", name)
	}
	if p.Cwd == "" {
		return out, fmt.Errorf("hook: %s payload has no cwd", name)
	}

	kind := ""
	var detail map[string]string
	switch name {
	case kernel.HookPostToolUse:
		switch {
		case isFileTool(p.ToolName):
			kind = kernel.EvEdit
		case p.ToolName == kernel.ToolBash:
			kind = kernel.EvEdit
			detail = map[string]string{"bash": "1"}
		}
	case kernel.HookStop:
		kind = kernel.EvTurnAnchored
	case kernel.HookSessionEnd:
		kind = kernel.EvSessionUsage
		detail = map[string]string{}
		if p.TranscriptPath != "" {
			detail[kernel.DetailTranscript] = p.TranscriptPath
		}
		if p.Reason != "" {
			detail[kernel.DetailReason] = p.Reason
		}
	}
	out.ev.Kind = kind
	out.ev.Detail = detail

	args := map[string]string{kernel.ArgCwd: p.Cwd}
	var repo objects.Repo
	var rerr error
	if known != nil {
		repo = *known
	} else {
		repo, rerr = objects.Open(p.Cwd)
	}
	switch {
	case rerr == nil:
		out.repo = repo
		args[kernel.ArgRepo] = repo.Root
		out.ev.Lane = repo.Branch
	case errors.Is(rerr, objects.ErrNotARepo):
		// Not a repository: nothing here is ours to record. Silent, not an error.
		out.ev.Kind, out.ev.Detail = "", nil
		kind = ""
	case kind != "":
		return out, rerr
	}
	if person {
		args[kernel.ArgPerson] = "1"
	}
	if kernel.IsShellTool(p.ToolName) && p.input("command") != "" {
		args[kernel.ArgCommand] = p.input("command")
	}
	out.ctx = kernel.Context{Tool: p.ToolName, Args: args}

	if !isFileTool(p.ToolName) {
		return out, nil
	}
	file := p.input("file_path")
	if file == "" {
		file = p.input("notebook_path")
	}
	if file == "" {
		if kind != "" {
			return out, fmt.Errorf("hook: %s payload has no tool_input.file_path", p.ToolName)
		}
		return out, nil
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(p.Cwd, file)
	}
	file = filepath.Clean(file)
	args[kernel.ArgFilePath] = file
	out.file = file
	rel, err := relIn(out.repo.Root, file)
	switch {
	case rerr != nil: // no repo: the context has the file, nothing is recorded
	case err != nil && kind != "":
		// A file outside the repo is not this repo's edit: record nothing.
		out.ev.Kind, out.ev.Detail = "", nil
	case err == nil:
		args[kernel.ArgPath] = rel
		if kind != "" {
			out.ev.Detail = map[string]string{kernel.DetailPath: rel}
		}
	}
	return out, nil
}

// relIn is file relative to root with forward slashes, or an error if file is
// not inside root. A symlinked or 8.3-shortened prefix is resolved before the
// answer is "outside".
func relIn(root, file string) (string, error) {
	inside := func(root, file string) (string, bool) {
		rel, ok := store.Within(root, file)
		return filepath.ToSlash(rel), ok
	}
	if rel, ok := inside(root, file); ok {
		return rel, nil
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		if f, err := filepath.EvalSymlinks(file); err == nil {
			if rel, ok := inside(r, f); ok {
				return rel, nil
			}
		}
	}
	return "", fmt.Errorf("hook: %s is outside the repository %s", file, root)
}
