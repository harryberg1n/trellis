package walls

import (
	"strings"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/mask"
)

// isPowerShell reports whether the tool reads its command line as PowerShell:
// a backslash is a path separator there, the escape is the backtick, a quote of
// either kind is doubled to be literal, and a here-string spans lines.
func isPowerShell(tool string) bool { return tool == kernel.ToolPowerShell }

// parse splits cmd into the simple commands the shell would run, and returns
// the command with its strings and comments blanked.
//
// The cut points are found on mask.Code's output, where the contents of
// strings and comments are blanks: a `;`, `&&`, `||`, `|`, `&` or newline
// inside a string or after a `#` can never split a command. The words are read
// from the text with its comments blanked and its strings kept, so a path in
// quotes is still the path. The two views are the same length, byte for byte.
func parse(tool, cmd string) (segs [][]word, masked string) {
	masked = mask.Code(tool, cmd)
	code := mask.Blank(tool, cmd, false, true)
	esc := byte('\\')
	if isPowerShell(tool) {
		esc = '`'
	}
	start := 0
	cut := func(end, next int) {
		if ws := tokenize(tool, code[start:end]); len(ws) > 0 {
			segs = append(segs, ws)
		}
		start = next
	}
	for i := 0; i < len(masked); i++ {
		switch masked[i] {
		case esc:
			i++ // the escaped byte is never an operator
		case '\n', ';':
			cut(i, i+1)
		case '|':
			next := i + 1
			if next < len(masked) && (masked[next] == '|' || masked[next] == '&') {
				next++
			}
			cut(i, next)
			i = next - 1
		case '&':
			if i > 0 && (masked[i-1] == '>' || masked[i-1] == '<') || i+1 < len(masked) && masked[i+1] == '>' {
				continue // 2>&1, >&2, &>file: a redirection, not an operator
			}
			next := i + 1
			if next < len(masked) && masked[next] == '&' {
				next++
			}
			cut(i, next)
			i = next - 1
		}
	}
	cut(len(masked), len(masked))
	return segs, masked
}

// segments is parse without the masked text.
func segments(tool, cmd string) [][]word {
	segs, _ := parse(tool, cmd)
	return segs
}

// tokenize reads the words of one simple command in the tool's own syntax.
func tokenize(tool, s string) []word {
	if isPowerShell(tool) {
		return tokenizePS(s)
	}
	return tokenizeBash(s)
}

// wordBuilder collects the text and the raw twin of a word.
type wordBuilder struct {
	words  []word
	cur    strings.Builder
	curRaw strings.Builder
	has    bool
}

func (b *wordBuilder) flush() {
	if b.has {
		b.words = append(b.words, word{text: b.cur.String(), raw: b.curRaw.String()})
		b.cur.Reset()
		b.curRaw.Reset()
		b.has = false
	}
}

// quoted adds a rune a quote or an escape produced.
func (b *wordBuilder) quoted(r rune) {
	b.cur.WriteRune(r)
	b.curRaw.WriteRune(escapedRune)
	b.has = true
}

// plain adds an ordinary rune.
func (b *wordBuilder) plain(r rune) {
	b.cur.WriteRune(r)
	b.curRaw.WriteRune(r)
	b.has = true
}

// tokenizeBash reads bash words: a backslash escapes the next rune (a
// backslash and a newline is a line continuation), single quotes are literal,
// double quotes allow a backslash escape.
func tokenizeBash(s string) []word {
	var b wordBuilder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\\' && i+1 < len(runes):
			i++
			if runes[i] != '\n' {
				b.quoted(runes[i])
			}
		case c == '\'':
			b.has = true
			for i++; i < len(runes) && runes[i] != '\''; i++ {
				b.quoted(runes[i])
			}
		case c == '"':
			b.has = true
			for i++; i < len(runes) && runes[i] != '"'; i++ {
				if runes[i] == '\\' && i+1 < len(runes) {
					i++
				}
				b.quoted(runes[i])
			}
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			b.flush()
		default:
			b.plain(c)
		}
	}
	b.flush()
	return b.words
}

// tokenizePS reads PowerShell words: a backslash is an ordinary rune, the
// backtick escapes the next rune, a single-quoted string is literal with ” for
// a quote, a double-quoted one has the backtick escape and "" for a quote, and
// a here-string @"..."@ or @'...'@ runs to its closer.
func tokenizePS(s string) []word {
	var b wordBuilder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '`' && i+1 < len(runes):
			i++
			b.quoted(runes[i])
		case c == '@' && !b.has && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\''):
			quote := runes[i+1]
			closer := string(quote) + "@"
			rest := string(runes[i+2:])
			end := strings.Index(rest, closer)
			if end < 0 {
				end = len(rest)
			}
			b.has = true
			for _, r := range strings.TrimPrefix(strings.TrimPrefix(rest[:end], "\r"), "\n") {
				b.quoted(r)
			}
			i += 2 + len([]rune(rest[:end])) + len([]rune(closer)) - 1
		case c == '\'':
			b.has = true
			for i++; i < len(runes); i++ {
				if runes[i] == '\'' {
					if i+1 >= len(runes) || runes[i+1] != '\'' {
						break
					}
					i++
				}
				b.quoted(runes[i])
			}
		case c == '"':
			b.has = true
			for i++; i < len(runes); i++ {
				if runes[i] == '`' && i+1 < len(runes) {
					i++
				} else if runes[i] == '"' {
					if i+1 >= len(runes) || runes[i+1] != '"' {
						break
					}
					i++
				}
				b.quoted(runes[i])
			}
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			b.flush()
		default:
			b.plain(c)
		}
	}
	b.flush()
	return b.words
}
