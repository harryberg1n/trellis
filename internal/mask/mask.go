// Package mask blanks the contents of string literals and comments in a shell
// command, replacing each masked byte with a space while preserving length,
// byte offsets and newlines, so a token that only appears inside a string or
// a comment never trips a wall. Detectors run on masked text.
//
// lex.go is copied from aphrollo-tools internal/mask at 12b3724e. This file
// keeps the idea of mask.go there (StringsAndComments over one lexer) without
// its language table: the rows are in shells.go.
package mask

import (
	"bytes"
	"unicode/utf8"
)

// rowFor is the lexer row of the tool that runs the command: PowerShell for
// the PowerShell tool, bash for anything else.
func rowFor(tool string) Language {
	if tool == "PowerShell" {
		return PowerShell()
	}
	return Bash()
}

// Blank is cmd read by the tool's row with the contents of its strings, its
// comments, or both blanked. The result is as long as cmd, byte for byte. A
// string's own quotes stay, so `echo "x"` masks to `echo " "`.
func Blank(tool, cmd string, strs, comments bool) string {
	return NewLexer(rowFor(tool)).Lex(cmd, strs, comments)
}

// Code is cmd with its strings and comments blanked, newlines inside strings
// included: every newline left is one that ends a command. As long as cmd.
func Code(tool, cmd string) string {
	lx := NewLexer(rowFor(tool))
	lx.StringNewlines = true
	return lx.Lex(cmd, true, true)
}

// charLiteralLen is copied from aphrollo-tools internal/mask/mask.go at
// 12b3724e. It returns the offset from b[i] of the quote closing the char
// literal that opens at b[i], and false when b[i] opens none. The body is one
// char, or a backslash and the escape it starts, whose tail runs to the next
// quote. The search stops at the line's end: a char literal never spans a
// line. No shell row sets CharLiteral; the lexer still calls it.
func charLiteralLen(b []byte, i int) (int, bool) {
	line, _, _ := bytes.Cut(b[i:], []byte{'\n'})
	j := 1
	if j < len(line) && line[j] == '\\' {
		j += 2 // the backslash and the byte it escapes, which may be a quote
		for j < len(line) && line[j] != '\'' {
			j++
		}
	} else {
		_, size := utf8.DecodeRune(line[j:])
		j += size
	}
	if j < len(line) && line[j] == '\'' {
		return j, true
	}
	return 0, false
}
