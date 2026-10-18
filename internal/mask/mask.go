// Package mask blanks the contents of string literals and comments in a shell
// command, replacing each masked byte with a space while preserving length,
// byte offsets and newlines, so a token that only appears inside a string or
// a comment never trips a wall. The walls use it to find where a command
// ends: segmentation runs on the masked text. They tokenize the words of each
// segment from the text with its strings kept and its quotes stripped.
//
// lex.go is copied from aphrollo-tools internal/mask at 12b3724e. This file
// keeps the idea of mask.go there (StringsAndComments over one lexer) without
// its language table: the rows are in shells.go.
package mask

import (
	"github.com/harryberg1n/trellis/internal/kernel"
)

// rowFor is the lexer row of the tool that runs the command: PowerShell for
// the PowerShell tool, bash for anything else.
func rowFor(tool string) Language {
	if tool == kernel.ToolPowerShell {
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
