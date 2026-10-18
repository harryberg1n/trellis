package hook

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// canonical maps a hook event as written on the command line ("PostToolUse",
// "post-tool-use", "posttooluse") to the harness's name for it. Besides the
// events the recorder records, it accepts the harness events that carry
// nothing to record yet, so wiring one up is not an error.
func canonical(event string) (string, bool) {
	switch strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(event)) {
	case "pretooluse":
		return "PreToolUse", true
	case "posttooluse":
		return "PostToolUse", true
	case "stop":
		return "Stop", true
	case "sessionstart":
		return "SessionStart", true
	case "sessionend":
		return "SessionEnd", true
	case "userpromptsubmit":
		return "UserPromptSubmit", true
	case "posttoolusefailure":
		return "PostToolUseFailure", true
	case "subagentstart":
		return "SubagentStart", true
	case "subagentstop":
		return "SubagentStop", true
	case "notification":
		return "Notification", true
	case "precompact":
		return "PreCompact", true
	}
	return "", false
}

// isFileTool reports whether tool's tool_input names the file it changes.
func isFileTool(tool string) bool {
	switch tool {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return true
	}
	return false
}

// parsed is a payload read as far as the effects need it.
type parsed struct {
	ev   kernel.Event
	ctx  kernel.Context
	repo Repo
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
// the repo with forward slashes) and command.
func Parse(event string, raw []byte) (kernel.Event, kernel.Context, error) {
	p, err := parse(event, raw)
	return p.ev, p.ctx, err
}

func parse(event string, raw []byte) (parsed, error) {
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
	if p.SessionID == "" {
		return out, fmt.Errorf("hook: %s payload has no session_id", name)
	}
	if p.Cwd == "" {
		return out, fmt.Errorf("hook: %s payload has no cwd", name)
	}

	kind := ""
	var detail map[string]string
	switch name {
	case "PostToolUse":
		switch p.ToolName {
		case "Edit", "Write", "MultiEdit", "NotebookEdit":
			kind = kernel.EvEdit
		case "Bash":
			kind = kernel.EvEdit
			detail = map[string]string{"bash": "1"}
		}
	case "Stop":
		kind = kernel.EvTurnAnchored
	case "SessionEnd":
		kind = kernel.EvSessionUsage
		detail = map[string]string{}
		if p.TranscriptPath != "" {
			detail["transcript"] = p.TranscriptPath
		}
		if p.Reason != "" {
			detail["reason"] = p.Reason
		}
	}
	out.ev.Kind = kind
	out.ev.Detail = detail

	args := map[string]string{"cwd": p.Cwd}
	repo, rerr := Locate(p.Cwd)
	if rerr == nil {
		out.repo = repo
		args["repo"] = repo.Root
		out.ev.Lane = repo.Lane
	} else if kind != "" {
		return out, rerr
	}
	if p.ToolName == "Bash" && p.ToolInput.Command != "" {
		args["command"] = p.ToolInput.Command
	}
	out.ctx = kernel.Context{Tool: p.ToolName, Args: args}

	if !isFileTool(p.ToolName) {
		return out, nil
	}
	file := p.ToolInput.FilePath
	if file == "" {
		file = p.ToolInput.NotebookPath
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
	args["file_path"] = file
	out.file = file
	rel, err := relIn(out.repo.Root, file)
	switch {
	case rerr != nil: // no repo: the context has the file, nothing is recorded
	case err != nil && kind != "":
		return out, err
	case err == nil:
		args["path"] = rel
		if kind != "" {
			out.ev.Detail = map[string]string{"path": rel}
		}
	}
	return out, nil
}

// relIn is file relative to root with forward slashes, or an error if file is
// not inside root. A symlinked or 8.3-shortened prefix is resolved before the
// answer is "outside".
func relIn(root, file string) (string, error) {
	inside := func(root, file string) (string, bool) {
		rel, err := filepath.Rel(root, file)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return "", false
		}
		return filepath.ToSlash(rel), true
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
