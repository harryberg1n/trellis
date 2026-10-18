package install

import (
	"strings"
	"testing"
)

func TestFinish_RefusesInvalidOutput(t *testing.T) {
	if _, err := finish([]byte(`{"a":`), false); err == nil {
		t.Error("an invalid splice result was accepted")
	}
	got, err := finish([]byte(`{}`), true)
	if err != nil || string(got) != "\xEF\xBB\xBF{}" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestInstallThenEject_PreExistingContainersStayByteIdentical(t *testing.T) {
	for name, in := range map[string]string{
		"empty hooks object":    `{"a":1,"hooks":{}}`,
		"empty event array":     `{"hooks":{"PreToolUse": []}}`,
		"spaced empty object":   `{ }`,
		"spaced hooks object":   "{\n  \"a\": 1,\n  \"hooks\": { }\n}\n",
		"spaced event array":    "{\n  \"hooks\": {\n    \"Stop\": [ ]\n  }\n}\n",
		"blank file object":     "{}\n",
		"foreign sole in event": `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"f"}]}]}}`,
	} {
		added, _, err := addEntries([]byte(in), wantT("PreToolUse", "Stop"), ownedT)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		back, _, err := removeEntries(added, ownedT)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(back) != in {
			t.Errorf("%s: install then eject gave\n%q\nwant\n%q\n(installed: %q)", name, back, in, added)
		}
	}
}

func TestAddEntries_MixedEntryKeepsItsForeignHook(t *testing.T) {
	const in = `{"hooks":{"Stop":[{"matcher":"m","hooks":[{"type":"command","command":"f"},{"type":"command","command":"T-old","timeout":10}]}]}}`
	const want = `{"hooks":{"Stop":[{"matcher":"m","hooks":[{"type":"command","command":"f"},{"type":"command","command":"T-stop","timeout":10}]}]}}`
	got, actions, err := addEntries([]byte(in), wantT("Stop"), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want || strings.Join(actions, ",") != "update hook Stop" {
		t.Errorf("got %s %v", got, actions)
	}
	const wantBack = `{"hooks":{"Stop":[{"matcher":"m","hooks":[{"type":"command","command":"f"}]}]}}`
	back, actions, err := removeEntries([]byte(in), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != wantBack || strings.Join(actions, ",") != "remove hook Stop" {
		t.Errorf("eject gave %s %v", back, actions)
	}
}

func TestRemoveEntries_EntryWithOnlyTrellisHooksGoesWhole(t *testing.T) {
	const in = `{"hooks":{"Stop":[{"hooks":[{"command":"f"}]},{"hooks":[{"command":"T-a"},{"command":"T-b"}]}]}}`
	const want = `{"hooks":{"Stop":[{"hooks":[{"command":"f"}]}]}}`
	got, _, err := removeEntries([]byte(in), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("got %s", got)
	}
}

func TestAddEntries_CRLFDocumentGetsNoBareLF(t *testing.T) {
	const in = "{\r\n  \"a\": 1,\r\n  \"hooks\": {\r\n    \"Stop\": [\r\n      {\"hooks\": []}\r\n    ]\r\n  }\r\n}\r\n"
	got, _, err := addEntries([]byte(in), wantT("Stop", "SessionStart"), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if n, crlf := strings.Count(string(got), "\n"), strings.Count(string(got), "\r\n"); n != crlf || n < 10 {
		t.Errorf("%d LF, %d CRLF in %q", n, crlf, got)
	}
	back, _, err := removeEntries(got, ownedT)
	if err != nil || string(back) != in {
		t.Errorf("eject gave %q, %v", back, err)
	}
}

func TestAddEntries_BOMIsPreserved(t *testing.T) {
	const in = "\xEF\xBB\xBF{\"a\":1}\n"
	got, _, err := addEntries([]byte(in), wantT("Stop"), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "\xEF\xBB\xBF{\"a\":1,\"hooks\":") {
		t.Errorf("got %q", got)
	}
	back, _, err := removeEntries(got, ownedT)
	if err != nil || string(back) != in {
		t.Errorf("eject gave %q, %v", back, err)
	}
}
