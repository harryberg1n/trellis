package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
)

func TestPluginManifest_HooksMirrorTheInstall(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc settingsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	const tools = "Edit|Write|Bash|PowerShell"
	wantMatcher := map[string]string{
		"PreToolUse": tools, "PostToolUse": tools,
		"Stop": "", "SessionStart": "", "SessionEnd": "", "UserPromptSubmit": "",
	}
	if len(doc.Hooks) != len(wantMatcher) {
		t.Errorf("hooks.json has %d events, want %d", len(doc.Hooks), len(wantMatcher))
	}
	for ev, matcher := range wantMatcher {
		entries := doc.Hooks[ev]
		if len(entries) != 1 || len(entries[0].Hooks) != 1 {
			t.Errorf("%s: %+v", ev, entries)
			continue
		}
		h := entries[0].Hooks[0]
		if entries[0].Matcher != matcher || h.Type != "command" || h.Command != "trellis hook "+ev || h.Timeout != 10 {
			t.Errorf("%s: matcher %q, hook %+v", ev, entries[0].Matcher, h)
		}
	}
	// The per-repo install writes the same events and matchers.
	for _, w := range wanted("/s/bin/v/trellis") {
		if w.Entry.Matcher != wantMatcher[w.Event] || w.Entry.Hooks[0].Timeout != 10 {
			t.Errorf("install differs from the manifest for %s: %+v", w.Event, w.Entry)
		}
	}
}

func TestPluginManifest_PluginJSONNamesTrellis(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]string
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p["name"] != "trellis" || p["version"] == "" || p["description"] == "" {
		t.Errorf("plugin.json: %v", p)
	}
	version, err := os.ReadFile(filepath.Join("..", "..", "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(version)), p["version"]; got != want {
		t.Errorf("VERSION is %q, plugin.json version is %q; they must match", got, want)
	}
}

func TestRelease_TagMustMatchTheVersionFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`cat VERSION`, `"v${version}" != "$GITHUB_REF_NAME"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("release.yml lacks the VERSION check %q", want)
		}
	}
}

func TestInstall_WiresExactlyTheEventsTheKernelNames(t *testing.T) {
	want := []string{"PreToolUse", "PostToolUse", "Stop", "SessionStart", "SessionEnd", "UserPromptSubmit"}
	if got := kernel.InstalledHookEvents(); !slices.Equal(got, want) {
		t.Errorf("kernel.InstalledHookEvents = %v, want %v", got, want)
	}
	entries := wanted("/s/bin/v/trellis")
	got := make([]string, 0, len(entries))
	for _, w := range entries {
		got = append(got, w.Event)
	}
	if !slices.Equal(got, want) {
		t.Errorf("install wires %v, want %v", got, want)
	}
}
