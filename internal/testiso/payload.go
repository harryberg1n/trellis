package testiso

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
)

// JSONString is s as a JSON string literal, quotes included. Control
// characters, quotes and backslashes are escaped, so a Windows path or a
// content full of newlines stays valid JSON.
func JSONString(s string) string {
	b, err := json.Marshal(s)
	if err != nil { // strings always marshal
		panic(err)
	}
	return string(b)
}

// PayloadOption changes the payload HookPayload builds.
type PayloadOption func(map[string]any)

// AsPerson makes the payload a person's: a git hook of their own writes no
// session_id and no hook_event_name, which the harness always sets.
func AsPerson() PayloadOption {
	return func(m map[string]any) { delete(m, "hook_event_name") }
}

// WithSession puts a session id in the payload.
func WithSession(id string) PayloadOption {
	return func(m map[string]any) { m["session_id"] = id }
}

// HookPayload is the JSON a harness hands a hook: the event, the tool and its
// input map, run in cwd.
func HookPayload(t testing.TB, event, tool, cwd string, input map[string]string, opts ...PayloadOption) []byte {
	t.Helper()
	m := map[string]any{"cwd": cwd, "hook_event_name": event, "tool_name": tool, "tool_input": input}
	for _, o := range opts {
		o(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Fill expands the placeholders of a recorded payload shape. Placeholders are
// written @NAME@ and every value is JSON-escaped. It is the one placeholder
// scheme of the module's payload fixtures.
func Fill(shape []byte, vars map[string]string) []byte {
	out := string(shape)
	// Sorted, so a value that itself holds another placeholder expands the
	// same way on every run.
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		quoted := JSONString(vars[k])
		out = strings.ReplaceAll(out, k, quoted[1:len(quoted)-1])
	}
	return []byte(out)
}
