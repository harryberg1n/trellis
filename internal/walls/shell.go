package walls

// The shell reading in this file is adapted from aphrollo-tools
// internal/tdd/shell/shellwrite.go and tilde.go at 12b3724e (shellWordTokens,
// shellSegmentsTokens, stripHeredocBodies, splitRedirect, verbTargets,
// resolveAgainst, expandTilde) and from internal/tdd/postedit/discardbash.go
// (commandSubstitutionBodies, bashDashCScript). Incidents: #725, a merge
// marker inside a quoted grep pattern read as a redirect; #849, a leading
// `~` joined onto the working directory; #118, a write behind a `cd`. The
// adaptations: no runtime.GOOS test, no write-verb table beyond the four
// shapes the primary-write wall needs, and a walk that hands each simple
// command to a callback together with the directory it runs in.
//
// It is not a shell parser. Variable expansion and globbing are not
// evaluated; a command hidden behind either is missed, which is the fail-open
// direction a wall owes: a false deny wedges a session.

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/harryberg1n/trellis/internal/mask"
)

// word is one shell word: text is the argument the program receives (quotes
// stripped, escapes resolved); raw is as long, with every rune a quote or an
// escape produced replaced by escapedRune, so `">file"` never reads as a
// redirect.
type word struct{ text, raw string }

// escapedRune is raw's stand-in for a quoted or escaped rune. It is neither a
// digit nor `>`.
const escapedRune = '\x00'

func texts(ws []word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.text
	}
	return out
}

// tokenize is the one character-level scanner: words and the operators that
// end a simple command (`;`, `&&`, `||`, `|`, `&`, `|&`, a newline).
func tokenize(cmd string) []word {
	var (
		words  []word
		cur    strings.Builder
		curRaw strings.Builder
		has    bool
	)
	flush := func() {
		if has {
			words = append(words, word{text: cur.String(), raw: curRaw.String()})
			cur.Reset()
			curRaw.Reset()
			has = false
		}
	}
	op := func(s string) { words = append(words, word{text: s, raw: s}) }
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
			curRaw.WriteRune(escapedRune)
			has = true
		case c == '\'' || c == '"':
			quote := c
			has = true
			for i++; i < len(runes) && runes[i] != quote; i++ {
				if quote == '"' && runes[i] == '\\' && i+1 < len(runes) {
					i++
				}
				cur.WriteRune(runes[i])
				curRaw.WriteRune(escapedRune)
			}
		case c == '\n':
			flush()
			op("\n")
		case c == ' ' || c == '\t' || c == '\r':
			flush()
		case c == ';':
			flush()
			op(";")
		case c == '&' || c == '|':
			flush()
			s := string(c)
			if i+1 < len(runes) && (runes[i+1] == c || (c == '|' && runes[i+1] == '&')) {
				i++
				s += string(runes[i])
			}
			op(s)
		default:
			cur.WriteRune(c)
			curRaw.WriteRune(c)
			has = true
		}
	}
	flush()
	return words
}

// segments splits cmd into the simple commands a shell would run.
func segments(cmd string) [][]word {
	var segs [][]word
	var cur []word
	for _, w := range tokenize(cmd) {
		switch w.text {
		case ";", "&&", "||", "|", "&", "\n", "|&":
			if len(cur) > 0 {
				segs = append(segs, cur)
			}
			cur = nil
		default:
			cur = append(cur, w)
		}
	}
	if len(cur) > 0 {
		segs = append(segs, cur)
	}
	return segs
}

var heredocOpener = regexp.MustCompile(`<<-?[ \t]*(?:'([^']*)'|"([^"]*)"|([A-Za-z_][A-Za-z0-9_]*))`)

// stripHeredocBodies removes the lines a heredoc feeds to a command, keeping
// the line that opens it. A body is data. A heredoc whose terminator never
// appears is left alone.
func stripHeredocBodies(cmd string) string {
	out, _ := splitHeredocs(cmd)
	return out
}

// splitHeredocs is cmd without its heredoc bodies and the bodies, joined.
func splitHeredocs(cmd string) (code, bodies string) {
	if !strings.Contains(cmd, "<<") {
		return cmd, ""
	}
	lines := strings.Split(cmd, "\n")
	var out, body []string
	for i := 0; i < len(lines); i++ {
		out = append(out, lines[i])
		for _, m := range heredocOpener.FindAllStringSubmatch(lines[i], -1) {
			delim := firstNonEmpty(m[1:])
			end := terminatorLine(lines, i+1, delim)
			if end < 0 {
				continue
			}
			body = append(body, lines[i+1:end]...)
			i = end
		}
	}
	return strings.Join(out, "\n"), strings.Join(body, "\n")
}

func firstNonEmpty(ss []string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func terminatorLine(lines []string, from int, delim string) int {
	for i := from; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == delim {
			return i
		}
	}
	return -1
}

// baseCommand is the program a word names, without its directory or an .exe
// suffix: `/usr/bin/sed` and `sed.exe` are both `sed`.
func baseCommand(w string) string {
	name := filepath.Base(filepath.FromSlash(w))
	if lower := strings.ToLower(name); strings.HasSuffix(lower, ".exe") {
		return lower[:len(lower)-len(".exe")]
	}
	return name
}

// maxDepth bounds how far a walk follows `bash -c` and `$(...)`.
const maxDepth = 4

// walk calls fn with every simple command cmd runs and the directory it runs
// in: cwd, moved by a `cd` segment for the segments after it. It follows the
// script of a `<shell> -c` segment and the body of each unquoted `$(...)`.
// With keepBodies set, heredoc bodies are scanned as part of the command (a
// commit message is one); otherwise they are data and dropped.
func walk(tool, cmd, cwd string, keepBodies bool, fn func(seg []word, dir string)) {
	walkDepth(tool, cmd, cwd, keepBodies, 0, fn)
}

func walkDepth(tool, cmd, cwd string, keepBodies bool, depth int, fn func([]word, string)) {
	if depth > maxDepth || strings.TrimSpace(cmd) == "" {
		return
	}
	src := cmd
	if !keepBodies {
		src = stripHeredocBodies(cmd)
	}
	dir := cwd
	for _, seg := range segments(src) {
		if target, ok := cdTarget(seg); ok {
			dir = resolve(dir, target)
			continue
		}
		fn(seg, dir)
		if script, ok := dashCScript(seg); ok {
			walkDepth(tool, script, dir, keepBodies, depth+1, fn)
		}
	}
	masked := mask.ForTool(tool, src)
	for _, body := range substitutionBodies(src, masked) {
		walkDepth(tool, body, dir, keepBodies, depth+1, fn)
	}
}

// cdTarget is the directory a `cd` segment moves into, and whether the segment
// is a `cd`. The directory is "" for a cd this scanner cannot resolve (a bare
// `cd`, `cd -`, `cd ~user`): later relative paths are then dropped, not
// resolved against the wrong directory.
func cdTarget(seg []word) (string, bool) {
	if len(seg) == 0 || baseCommand(seg[0].text) != "cd" {
		return "", false
	}
	if len(seg) > 1 && !strings.HasPrefix(seg[1].text, "-") {
		if p, ok := expandTilde(seg[1].raw, seg[1].text); ok {
			return p, true
		}
	}
	return "", true
}

// dashCScript is the script a `bash -c`, `sh -c` or `zsh -c` segment runs.
func dashCScript(seg []word) (string, bool) {
	if len(seg) == 0 {
		return "", false
	}
	switch baseCommand(seg[0].text) {
	case "bash", "sh", "zsh":
	default:
		return "", false
	}
	for i := 1; i+1 < len(seg); i++ {
		if seg[i].text == "-c" {
			return seg[i+1].text, true
		}
	}
	return "", false
}

// substitutionBodies is the text of every `$(...)` that src runs. masked is
// src with its strings blanked: a `$(` inside single quotes is data. Nested
// parentheses are balanced.
func substitutionBodies(src, masked string) []string {
	var bodies []string
	for i := 0; i+1 < len(masked); i++ {
		if masked[i] != '$' || masked[i+1] != '(' {
			continue
		}
		depth := 1
		j := i + 2
		for ; j < len(masked) && depth > 0; j++ {
			switch masked[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth == 0 {
			bodies = append(bodies, src[i+2:j-1])
			i = j - 1
		}
	}
	return bodies
}

// resolve turns a command's path operand into an absolute path: "" for the
// null sink, for an operand with shell syntax this file does not evaluate, and
// for a relative operand with no known directory.
func resolve(cwd, p string) string {
	if p == "" || strings.ContainsAny(p, "$`") || isNullDevice(p) {
		return ""
	}
	rooted := strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if cwd == "" {
		return ""
	}
	if rooted {
		return filepath.Clean(filepath.Join(filepath.VolumeName(cwd)+string(filepath.Separator), p))
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

func isNullDevice(p string) bool {
	base := strings.ToLower(filepath.Base(filepath.FromSlash(p)))
	if base == "nul" || base == "nul:" {
		return true
	}
	norm := strings.ToLower(path.Clean(filepath.ToSlash(p)))
	return norm == "/dev/null" || norm == "dev/null" || strings.HasSuffix(norm, "/dev/null")
}

// expandTilde expands an unquoted leading `~` or `~/` to HOME; a quoted one is
// the literal relative path. ok is false for `~user` and when HOME is unknown.
func expandTilde(raw, text string) (string, bool) {
	if r := []rune(raw); len(r) == 0 || r[0] == escapedRune {
		return text, true
	}
	switch {
	case text == "~", strings.HasPrefix(text, "~/"):
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", false
		}
		if text == "~" {
			return home, true
		}
		return filepath.Join(home, filepath.FromSlash(text[2:])), true
	case strings.HasPrefix(text, "~"):
		return "", false
	}
	return text, true
}

// writeTargets names every path one simple command would write, resolved
// against dir: redirections wherever they appear, the files of `sed -i` and
// `tee`, the destination of `cp`, `mv` and `install`, and the file of the
// PowerShell cmdlets Set-Content, Add-Content and Out-File.
func writeTargets(seg []word, dir string) []string {
	var raw []string
	var rest []word
	skip := false
	for i, w := range seg {
		if skip {
			skip = false
			continue
		}
		target, targetRaw, wantsNext, ok := splitRedirect(w)
		switch {
		case !ok:
			rest = append(rest, w)
		case target != "":
			if p, keep := expandTilde(targetRaw, target); keep {
				raw = append(raw, p)
			}
		case wantsNext && i+1 < len(seg):
			nxt := seg[i+1]
			if p, keep := expandTilde(nxt.raw, nxt.text); keep {
				raw = append(raw, p)
			}
			skip = true
		}
	}
	raw = append(raw, verbTargets(rest)...)
	var out []string
	for _, p := range raw {
		if abs := resolve(dir, p); abs != "" {
			out = append(out, abs)
		}
	}
	return out
}

// splitRedirect reads an output redirection out of one word: `>`, `>>` or
// `>|`, optionally after a file descriptor, the target attached or in the next
// word. `2>&1` writes no file. The operator is read off raw, so a quoted `>`
// is no redirect.
func splitRedirect(w word) (target, targetRaw string, wantsNext, ok bool) {
	raw := []rune(w.raw)
	i := -1
	for k, c := range raw {
		if c == '>' {
			i = k
			break
		}
	}
	if i < 0 {
		return "", "", false, false
	}
	for _, c := range raw[:i] {
		if c < '0' || c > '9' {
			return "", "", false, false
		}
	}
	j := i + 1
	if j < len(raw) && raw[j] == '>' {
		j++
	}
	if j < len(raw) && raw[j] == '|' {
		j++
	}
	if j < len(raw) && raw[j] == '&' {
		return "", "", false, true
	}
	text := []rune(w.text)
	if j >= len(text) {
		return "", "", true, true
	}
	return string(text[j:]), string(raw[j:]), false, true
}

// verbTargets names the operands a write verb would write.
func verbTargets(words []word) []string {
	if len(words) == 0 {
		return nil
	}
	verb := baseCommand(words[0].text)
	args := words[1:]
	switch verb {
	case "sed":
		if !hasInPlace(texts(args)) {
			return nil
		}
		operands := operandsOf(args, map[string]bool{"-e": true, "-f": true, "--expression": true, "--file": true})
		if !hasScriptFlag(texts(args)) && len(operands) > 0 {
			operands = operands[1:]
		}
		return expandAll(operands)
	case "tee":
		return expandAll(operandsOf(args, nil))
	case "cp", "mv", "install":
		for i, a := range args {
			if (a.text == "-t" || a.text == "--target-directory") && i+1 < len(args) {
				return expandAll(args[i+1 : i+2])
			}
			if v, ok := strings.CutPrefix(a.text, "--target-directory="); ok {
				return []string{v}
			}
		}
		operands := operandsOf(args, map[string]bool{"-t": true, "--target-directory": true})
		if len(operands) < 2 {
			return nil
		}
		return expandAll(operands[len(operands)-1:])
	}
	switch {
	case strings.EqualFold(verb, "Set-Content"), strings.EqualFold(verb, "Add-Content"):
		return expandAll(psFile(args, "-Path", "-LiteralPath"))
	case strings.EqualFold(verb, "Out-File"):
		return expandAll(psFile(args, "-FilePath", "-Path", "-LiteralPath"))
	}
	return nil
}

func expandAll(ws []word) []string {
	var out []string
	for _, w := range ws {
		if p, ok := expandTilde(w.raw, w.text); ok {
			out = append(out, p)
		}
	}
	return out
}

// psFile is a PowerShell cmdlet's file operand: the value of whichever named
// parameter was written (any case), else the first positional operand.
func psFile(args []word, flags ...string) []word {
	for i, a := range args {
		for _, f := range flags {
			if strings.EqualFold(a.text, f) && i+1 < len(args) {
				return []word{args[i+1]}
			}
		}
	}
	for _, a := range args {
		if !strings.HasPrefix(a.text, "-") {
			return []word{a}
		}
	}
	return nil
}

func hasInPlace(args []string) bool {
	for _, a := range args {
		if a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "i") {
			return true
		}
	}
	return false
}

func hasScriptFlag(args []string) bool {
	for _, a := range args {
		switch {
		case a == "-e" || a == "-f" || a == "--expression" || a == "--file":
			return true
		case strings.HasPrefix(a, "--expression=") || strings.HasPrefix(a, "--file="):
			return true
		}
	}
	return false
}

func operandsOf(args []word, takesValue map[string]bool) []word {
	var out []word
	skip := false
	for i, a := range args {
		if skip {
			skip = false
			continue
		}
		if !strings.HasPrefix(a.text, "-") || a.text == "-" {
			out = append(out, a)
			continue
		}
		if takesValue[a.text] && i+1 < len(args) {
			skip = true
		}
	}
	return out
}

// gitCall is one git invocation: the directory it runs in (cwd, moved by `cd`
// and `-C`), the `-c` settings before the subcommand, the subcommand and its
// arguments.
type gitCall struct {
	Dir     string
	Globals []string
	Verb    string
	Args    []string
}

// prefixes are the words that run the command after them.
var prefixes = map[string]bool{"env": true, "command": true, "exec": true, "time": true, "nohup": true, "sudo": true}

// parseGit reads a segment as a git call, or reports that it is not one. git's
// own leading options are read out of the way, so `git -C d -c k=v commit` is
// a commit in d with the setting k=v.
func parseGit(seg []word, dir string) (gitCall, bool) {
	i := 0
	for i < len(seg) {
		t := seg[i].text
		if prefixes[baseCommand(t)] || strings.Contains(t, "=") && !strings.HasPrefix(t, "-") {
			i++
			continue
		}
		break
	}
	if i >= len(seg) || baseCommand(seg[i].text) != "git" {
		return gitCall{}, false
	}
	call := gitCall{Dir: dir}
	for i++; i < len(seg); i++ {
		a := seg[i].text
		switch {
		case !strings.HasPrefix(a, "-"):
			call.Verb = a
			call.Args = texts(seg[i+1:])
			return call, true
		case a == "-C" && i+1 < len(seg):
			i++
			call.Dir = resolve(call.Dir, seg[i].text)
		case a == "-c" && i+1 < len(seg):
			i++
			call.Globals = append(call.Globals, seg[i].text)
		case (a == "--git-dir" || a == "--work-tree" || a == "--namespace") && i+1 < len(seg):
			i++
		}
	}
	return gitCall{}, false
}
