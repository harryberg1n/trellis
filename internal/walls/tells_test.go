package walls

import "testing"

func TestTell_AttributionShapesOnly(t *testing.T) {
	deny := map[string]string{
		"fix\n\nCo-Authored-By: X <x@y>":                                   "an attribution trailer (Co-Authored-By)",
		"fix\n\nco-authored-by: Claude <noreply@anthropic.com>":            "an attribution trailer (co-authored-by)",
		"fix\n\nGenerated-By: Claude Code":                                 "an attribution trailer (Generated-By)",
		"fix\n\nAssisted-By: gpt-4":                                        "an attribution trailer (Assisted-By)",
		"fix\n\nGenerated-By: bot <bot@openai.com>":                        "an attribution trailer (Generated-By)",
		"fix\n\nClaude-Session: https://claude.ai/code/session_1":          "an attribution trailer (Claude-Session)",
		"\U0001F916 Generated with [Claude Code](https://claude.com/code)": "an attribution footer (\"\U0001F916 Generated with [Claude\")",
		"body\n\nGenerated with Cursor":                                    "an attribution footer (\"Generated with Cursor\")",
		"see https://claude.ai/code/session_9":                             "an attribution footer (\"https://claude.ai/code\")",
		"written by Claude":                                                "a tool or model name (\"Claude\")",
		"thanks, ChatGPT":                                                  "a tool or model name (\"ChatGPT\")",
		"I used Copilot for this":                                          "a tool or model name (\"Copilot\")",
		"ask Sonnet 5.5":                                                   "a tool or model name (\"Sonnet 5\")",
		"Claude wrote `foo` and `bar`":                                     "a tool or model name (\"Claude\")",
	}
	for text, want := range deny {
		if got := tell(text); got != want {
			t.Errorf("tell(%q) = %q, want %q", text, got, want)
		}
	}
	allow := []string{
		"fix the parser",
		"bump claude-api to 2.0",
		"use claude-fable-5-1 in the tests",
		"bump anthropic sdk",
		"auto-generated with sqlc",
		"Generated with sqlc",
		"\U0001F916 robots in the title",
		"Generated-By: protoc",
		"Assisted-By: a colleague",
		"see `claude` for details",
		"read ~/.claude/settings.json",
		"docs at https://claude.ai/docs",
		"see https://example.com/claude/x",
		"the claude_api module",
		"opusculum and coauthored by nobody",
		"an opus of haiku",
	}
	for _, text := range allow {
		if got := tell(text); got != "" {
			t.Errorf("tell(%q) = %q, want it allowed", text, got)
		}
	}
}
