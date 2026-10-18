package testiso

import (
	"encoding/json"
	"testing"
)

func TestJSONString_EscapesWhatJSONNeeds(t *testing.T) {
	if got, want := JSONString("a\"b\\c\nd\x01"), `"a\"b\\c\nd\u0001"`; got != want {
		t.Errorf("JSONString = %s, want %s", got, want)
	}
}

func TestHookPayload_CarriesTheEventToolAndInput(t *testing.T) {
	got := string(HookPayload(t, "PreToolUse", "Bash", `C:\w`, map[string]string{"command": "ls"}, WithSession("s-1")))
	want := `{"cwd":"C:\\w","hook_event_name":"PreToolUse","session_id":"s-1","tool_input":{"command":"ls"},"tool_name":"Bash"}`
	if got != want {
		t.Errorf("payload %s, want %s", got, want)
	}
	var m map[string]any
	if err := json.Unmarshal(HookPayload(t, "Stop", "", "/w", nil), &m); err != nil || m["session_id"] != nil {
		t.Errorf("a payload with no session: %v, %v; want no session_id", m, err)
	}
}

func TestFill_EscapesEveryValue(t *testing.T) {
	got := string(Fill([]byte(`{"cwd":"@CWD@","c":"@C@"}`), map[string]string{"@CWD@": `C:\w`, "@C@": "a\nb"}))
	if want := `{"cwd":"C:\\w","c":"a\nb"}`; got != want {
		t.Errorf("Fill = %s, want %s", got, want)
	}
}
