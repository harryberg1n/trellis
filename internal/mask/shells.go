package mask

// Bash is the row of aphrollo-tools internal/lang/languages/shell.toml at
// 12b3724e, written as a Go value: `#` opens a comment only at the start of a
// word, a backslash outside a string escapes the next byte, a single-quoted
// string has no escapes and a double-quoted one has the backslash. Incident:
// a quoted `git reset --hard` or `--no-verify` in an echo or a commit message
// was read as the command it names.
func Bash() Language {
	return Language{
		Name:         "bash",
		CodeEscape:   true,
		LineComments: []LineComment{{Marker: "#", WordStart: true}},
		Strings: []StringForm{
			{ID: "single", Open: "'", Close: "'", Escape: EscapeNone, Multiline: true},
			{ID: "double", Open: `"`, Close: `"`, Escape: EscapeBackslash, Multiline: true},
		},
	}
}

// PowerShell is the row for the PowerShell tool. aphrollo-tools 12b3724e has
// no PowerShell row, so this one is written here, not copied: `#` opens a
// comment at the start of a word and `<# #>` a block comment, a quote of
// either kind is escaped by doubling it (a backslash is a path separator, not
// an escape), and a here-string `@" "@` or `@' '@` runs across lines. The
// backtick escape is not modelled: a backtick-escaped quote ends its string
// early, which exposes text and never hides it.
func PowerShell() Language {
	return Language{
		Name:          "powershell",
		LineComments:  []LineComment{{Marker: "#", WordStart: true}},
		BlockComments: []BlockComment{{Open: "<#", Close: "#>"}},
		Strings: []StringForm{
			{ID: "here-double", Open: `@"`, Close: `"@`, Escape: EscapeNone, Multiline: true},
			{ID: "here-single", Open: `@'`, Close: `'@`, Escape: EscapeNone, Multiline: true},
			{ID: "single", Open: "'", Close: "'", Escape: EscapeDoubling, Multiline: true},
			{ID: "double", Open: `"`, Close: `"`, Escape: EscapeDoubling, Multiline: true},
		},
	}
}
