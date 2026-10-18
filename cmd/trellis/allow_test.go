package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestAllow_WaivesTheNextDenyOnce(t *testing.T) {
	bin := build(t)
	repo := testiso.Repo(t)
	state := filepath.Join(t.TempDir(), "state")
	payload := string(testiso.HookPayload(t, kernel.HookPreToolUse, kernel.ToolBash, repo,
		map[string]string{"command": "git push origin main"}, testiso.WithSession("s-1")))
	run := func(stdin string, args ...string) (int, string, string) {
		return runBin(t, bin, state, stdin, args, inDir(repo))
	}

	if code, _, errs := run(payload, "hook", "PreToolUse"); code != 2 || !strings.Contains(errs, "trellis: main-commit: ") {
		t.Fatalf("hook before allow: exit %d, stderr %q; want 2 and the main-commit line", code, errs)
	}
	if code, out, errs := run("", "allow", "main-commit"); code != 0 || out != "trellis: main-commit allowed once\n" || errs != "" {
		t.Fatalf("allow: exit %d, stdout %q, stderr %q", code, out, errs)
	}
	if code, out, errs := run(payload, "hook", "PreToolUse"); code != 0 || out != "" || errs != "" {
		t.Errorf("hook after allow: exit %d, stdout %q, stderr %q; want 0, empty, empty", code, out, errs)
	}
	if code, _, _ := run(payload, "hook", "PreToolUse"); code != 2 {
		t.Errorf("hook after the token was spent: exit %d, want 2", code)
	}
}

func TestAllow_RefusesRowsWithNoOverride(t *testing.T) {
	var out, errb bytes.Buffer
	for _, c := range []struct{ id, want string }{
		{"secrets", "has no override"},
		{"undercover", "has no override"},
		{"own-git-state", "has no override"},
		{"allow-people", "has no override"},
		{"no-such-row", "unknown row"},
	} {
		out.Reset()
		errb.Reset()
		if code := run([]string{"allow", c.id}, &out, &errb); code != 1 || !strings.Contains(errb.String(), c.want) || out.Len() != 0 {
			t.Errorf("allow %s: exit %d, stdout %q, stderr %q; want 1 and %q", c.id, code, out.String(), errb.String(), c.want)
		}
	}
	if code := run([]string{"allow"}, &out, &errb); code != 1 {
		t.Errorf("allow with no row: exit %d, want 1", code)
	}
}
