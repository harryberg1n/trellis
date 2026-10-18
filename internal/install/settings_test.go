package install

import (
	"bytes"
	"strings"
	"testing"
)

func ownedT(e Entry) bool {
	for _, h := range e.Hooks {
		if strings.HasPrefix(h.Command, "T-") {
			return true
		}
	}
	return false
}

func wantT(events ...string) []eventEntry {
	out := make([]eventEntry, 0, len(events))
	for _, ev := range events {
		out = append(out, eventEntry{Event: ev, Entry: Entry{Hooks: []Hook{{Type: "command", Command: "T-" + strings.ToLower(ev), Timeout: 10}}}})
	}
	return out
}

const oddDoc = `{ "a" : 1.0,
  "hooks" : {
    "Stop" : [ {"hooks":[{"type":"command","command":"f"}]} ]
  },
  "z":[1, 2]
}
`

const oddDocInstalled = `{ "a" : 1.0,
  "hooks" : {
    "Stop" : [ {"hooks":[{"type":"command","command":"f"}]}, {"hooks":[{"type":"command","command":"T-stop","timeout":10}]} ],
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "T-sessionstart",
            "timeout": 10
          }
        ]
      }
    ]
  },
  "z":[1, 2]
}
`

func TestAddEntries_SplicesWithoutTouchingForeignBytes(t *testing.T) {
	got, actions, err := addEntries([]byte(oddDoc), wantT("Stop", "SessionStart"), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != oddDocInstalled {
		t.Errorf("got:\n%s\nwant:\n%s", got, oddDocInstalled)
	}
	if want := "add hook Stop,add hook SessionStart"; strings.Join(actions, ",") != want {
		t.Errorf("actions %v, want %s", actions, want)
	}
}

func TestRemoveEntries_RestoresTheOriginalBytes(t *testing.T) {
	got, actions, err := removeEntries([]byte(oddDocInstalled), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != oddDoc {
		t.Errorf("got:\n%s\nwant:\n%s", got, oddDoc)
	}
	if want := "remove hook Stop,remove hook SessionStart"; strings.Join(actions, ",") != want {
		t.Errorf("actions %v, want %s", actions, want)
	}
}

func TestAddEntries_CompactDocWithoutHooks(t *testing.T) {
	const in = `{"a":1}`
	const want = `{"a":1,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"T-stop","timeout":10}]}]}}`
	got, _, err := addEntries([]byte(in), wantT("Stop"), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
	back, _, err := removeEntries(got, ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != in {
		t.Errorf("eject gave %s, want %s", back, in)
	}
}

func TestAddEntries_EmptyDocBecomesAnObject(t *testing.T) {
	const want = "{\n  \"hooks\": {\n    \"Stop\": [\n      {\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"T-stop\",\n            \"timeout\": 10\n          }\n        ]\n      }\n    ]\n  }\n}\n"
	got, _, err := addEntries([]byte("  \n"), wantT("Stop"), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAddEntries_RunTwiceKeepsAndNewerReplaces(t *testing.T) {
	once, _, err := addEntries([]byte(oddDoc), wantT("Stop"), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	again, actions, err := addEntries(once, wantT("Stop"), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, once) || strings.Join(actions, ",") != "keep hook Stop" {
		t.Errorf("second run changed the doc or said %v", actions)
	}
	newer := wantT("Stop")
	newer[0].Entry.Hooks[0].Command = "T-newer"
	repl, actions, err := addEntries(once, newer, ownedT)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{ "a" : 1.0,
  "hooks" : {
    "Stop" : [ {"hooks":[{"type":"command","command":"f"}]}, {"hooks":[{"type":"command","command":"T-newer","timeout":10}]} ]
  },
  "z":[1, 2]
}
`
	if string(repl) != want || strings.Join(actions, ",") != "update hook Stop" {
		t.Errorf("got:\n%s\nactions %v", repl, actions)
	}
}

func TestAddEntries_RefusesWhatItCannotParse(t *testing.T) {
	for name, in := range map[string]string{
		"not json":         `{"a":`,
		"comment":          "{ // c\n}",
		"top is an array":  `[]`,
		"hooks not object": `{"hooks":[]}`,
		"event not array":  `{"hooks":{"Stop":{}}}`,
	} {
		if _, _, err := addEntries([]byte(in), wantT("Stop"), ownedT); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestRemoveEntries_LeavesForeignAndEmptyThingsAlone(t *testing.T) {
	const in = `{"hooks":{"Stop":[]},"x":{}}`
	got, actions, err := removeEntries([]byte(in), ownedT)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != in || len(actions) != 0 {
		t.Errorf("got %s %v", got, actions)
	}
}

func TestWalkEntries_ListsEveryEntry(t *testing.T) {
	got, err := walkEntries([]byte(oddDocInstalled))
	if err != nil {
		t.Fatal(err)
	}
	cmds := make([]string, 0, len(got))
	for _, l := range got {
		cmds = append(cmds, l.Event+"="+l.Entry.Hooks[0].Command)
	}
	if want := "Stop=f,Stop=T-stop,SessionStart=T-sessionstart"; strings.Join(cmds, ",") != want {
		t.Errorf("got %v, want %s", cmds, want)
	}
}
