// The types below are the lexing subset of aphrollo-tools internal/lang
// (lang.go) at 12b3724e: Language, LineComment, BlockComment, StringForm and
// Escape, with their fields unchanged. The rest of that package (extensions,
// suppression directives, test patterns, the TOML loader) is not copied:
// trellis lexes shell command lines only, and its rows are Go values in
// shells.go.

package mask

// Escape is how a string form spells a literal delimiter or control byte.
type Escape string

const (
	// EscapeNone means nothing is special inside the string but its closer.
	EscapeNone Escape = "none"
	// EscapeBackslash means a backslash makes the byte after it part of the string.
	EscapeBackslash Escape = "backslash"
	// EscapeDoubling means two closers in a row are one literal closer and end
	// nothing, and a backslash is text.
	EscapeDoubling Escape = "doubling"
)

// Language is one row: how a language spells its comments and strings.
type Language struct {
	// Name is the row's identity.
	Name string
	// CodeEscape: a backslash outside a string escapes the byte after it, so
	// `don\'t` in shell opens no string.
	CodeEscape bool
	// LineExcept lists openers that begin with a line comment marker and are
	// code, not a comment.
	LineExcept []string

	LineComments  []LineComment
	BlockComments []BlockComment
	Strings       []StringForm
}

// LineComment is a marker that opens a comment ending at the line's end.
type LineComment struct {
	Marker string
	// WordStart: the marker opens a comment only where a word starts: at the
	// input's start, or after a blank, a newline or one of `;&|()<>`, so
	// `$#`, `${#a}` and `a#b` are code.
	WordStart bool
}

// BlockComment is an opener and closer pair.
type BlockComment struct {
	Open, Close string
	// Nested: an opener inside the comment opens another level that needs its
	// own closer.
	Nested bool
}

// StringForm is one way a language writes a string literal.
type StringForm struct {
	// ID is the form's name within its row, for messages.
	ID string
	// Open and Close delimit the literal; Close ends it on its first
	// occurrence (subject to Escape).
	Open, Close string
	Escape      Escape
	// Multiline: the literal runs across line ends. Otherwise it ends at its
	// line, which stops an unterminated quote from blanking the file below.
	Multiline bool
	// LineStart: the literal opens only where its line starts.
	LineStart bool
	// OpensAfter, when set or LineStart is, limits the opener to a line's
	// start or to a place whose nearest non-blank byte before it, on its
	// line, is one of these.
	OpensAfter string
	// BlankOpen: the opener is masked with the body.
	BlankOpen bool
	// CharLiteral: Open is a one-byte quote that starts a literal only when a
	// char literal's shape closes on the same line.
	CharLiteral bool
	// Heredoc: the literal is a heredoc or nowdoc; see Lexer.
	Heredoc bool
}
