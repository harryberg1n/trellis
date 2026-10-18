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
)

// payload is the part of a harness payload the recorder reads. Every hook
// gets the common fields; the rest depend on the event. Unknown fields are
// ignored, so a newer harness does not break the recorder.
type payload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	ToolName       string `json:"tool_name"`
	// ToolInput is kept raw: its fields depend on the tool, and an MCP tool may
	// put an object where Bash has a string. Read a field with input.
	ToolInput json.RawMessage `json:"tool_input"`
	Reason    string          `json:"reason"` // SessionEnd
}

// input is the string field key of tool_input, "" if tool_input is absent, is
// not an object, or has no string there.
func (p payload) input(key string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(p.ToolInput, &fields) != nil {
		return ""
	}
	var v string
	if json.Unmarshal(fields[key], &v) != nil {
		return ""
	}
	return v
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

// PayloadCwd is the cwd a payload names, "" if it names none or is not JSON.
// The command uses it to pick the repo before the store is opened.
func PayloadCwd(raw []byte) string {
	p, _ := decode(raw)
	return p.Cwd
}
