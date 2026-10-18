package walls

import (
	"fmt"
	"regexp"
	"strings"
)

// What an attribution looks like (2026-09 owner). The rule is narrow on
// purpose: undercover has no override, so a false deny cannot be waived.
var (
	// trailerTell is a trailer line. Co-Authored-By and Claude-Session are
	// attribution whatever they name; Generated-By and Assisted-By only when
	// the value names an AI tool or an AI vendor's address.
	trailerTell = regexp.MustCompile(`(?im)^[ \t]*(co-authored-by|claude-session|generated-by|assisted-by)[ \t]*:[ \t]*(.*)$`)
	aiValue     = regexp.MustCompile(`(?i)\b(claude|anthropic|chatgpt|openai|copilot|gemini|codex|cursor|gpt-?[0-9])\b|@(anthropic|openai)\.com`)
	// footerTell is a "Generated with <tool>" line, with or without the robot,
	// or the claude.ai/code link a tool appends.
	footerTell = regexp.MustCompile(`(?im)^[ \t]*(\x{1F916}[ \t]*)?generated with[ \t]+\[?(claude|copilot|chatgpt|gemini|cursor)|https?://claude\.(ai|com)/code`)
	// nameTell is a bare tool or model name used as a word.
	nameTell = regexp.MustCompile(`(?i)\b(claude|chatgpt|copilot|gemini|codex)\b|\b(sonnet|opus|haiku)[ \t]+[0-9]`)
)

// tell is the reason text must not be committed, or "".
func tell(text string) string {
	for _, m := range trailerTell.FindAllStringSubmatch(text, -1) {
		key := strings.ToLower(m[1])
		if key == "co-authored-by" || key == "claude-session" || aiValue.MatchString(m[2]) {
			return fmt.Sprintf("an attribution trailer (%s)", m[1])
		}
	}
	if m := footerTell.FindString(text); m != "" {
		return fmt.Sprintf("an attribution footer (%q)", strings.TrimSpace(m))
	}
	for _, loc := range nameTell.FindAllStringIndex(text, -1) {
		if !identifierLike(text, loc[0], loc[1]) {
			return fmt.Sprintf("a tool or model name (%q)", text[loc[0]:loc[1]])
		}
	}
	return ""
}

// identifierLike reports whether the match text[s:e] is part of a hyphenated
// identifier, a path, a URL or a host name, or sits in backticks: claude-api,
// ~/.claude/x, claude.ai, `claude`.
func identifierLike(text string, s, e int) bool {
	if s > 0 && strings.IndexByte("-_/.@:`", text[s-1]) >= 0 {
		return true
	}
	if e < len(text) {
		switch next := text[e]; {
		case next == '-' || next == '_' || next == '/':
			return true
		case next == '.' && e+1 < len(text) && isAlnum(text[e+1]) && (text[e-1] < '0' || text[e-1] > '9'):
			return true
		}
	}
	lineStart := strings.LastIndexByte(text[:s], '\n') + 1
	return strings.Count(text[lineStart:s], "`")%2 == 1
}

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
