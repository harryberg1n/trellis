// Package config reads a repository's trellis.toml: the file that turns
// trellis on, and the settings of its tree verbs. The reading covers the part
// of TOML the settings need (tables, quoted strings, lists of them, comments)
// with the standard library only, and refuses an unknown table or key with
// its line, so a typo does not silently change nothing.
package config

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// File is the name of the settings file at a checkout's root.
const File = "trellis.toml"

// Config is the settings of a repository.
type Config struct {
	Tree Tree
}

// Tree is [tree]: the tree verbs.
type Tree struct {
	// Remote is the remote the trees push to and are cut from (#50: a
	// checkout whose GitHub remote is called github). Default origin.
	Remote string
	New    New
	Merge  Merge
}

// New is [tree.new]: what `tree new` does to a new tree before it is used.
type New struct {
	// Link are paths of the primary checkout linked into the new tree, first
	// (#52: every lane started with `ln -s ../repo/node_modules`).
	Link []string
	// Run are commands run in the new tree after the links, shown as they
	// run (#50: the first check of every lane paid for setup).
	Run []string
	// Place is where a tree is put when no path is given: PlaceSibling, beside
	// the primary checkout, or PlaceInside, under it (#65: a session started
	// in the primary loads the nested CLAUDE.md files and path-scoped rules
	// only for files under its working directory).
	Place string
}

// The values of [tree.new] place.
const (
	PlaceSibling = "sibling"
	PlaceInside  = "inside"
)

// Merge is [tree.merge]: what `tree merge` waits for.
type Merge struct {
	// Checks is "auto" (wait for the checks a workflow can make) or "none"
	// (the gate is the local --check; never wait for CI, #53).
	Checks string
	// Require are check names that must appear on the pull request before
	// it merges (#52: during an Actions outage "no checks" meant the runs had
	// not started).
	Require []string
}

// Load reads the trellis.toml at dir. A missing file is the defaults.
func Load(dir string) (Config, error) {
	c := Config{Tree: Tree{Remote: "origin", New: New{Place: PlaceSibling}, Merge: Merge{Checks: "auto"}}}
	raw, err := os.ReadFile(filepath.Join(dir, File))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	p := parser{lines: strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n"), seen: map[string]bool{}}
	if err := p.parse(&c); err != nil {
		return c, err
	}
	if c.Tree.Merge.Checks == "none" && len(c.Tree.Merge.Require) > 0 {
		return c, fmt.Errorf("%s: tree.merge: checks = \"none\" and require exclude each other", File)
	}
	return c, nil
}

type parser struct {
	lines []string
	i     int // the index of the line being read
	seen  map[string]bool
}

func (p *parser) errf(format string, a ...any) error {
	return fmt.Errorf("%s:%d: %s", File, p.i+1, fmt.Sprintf(format, a...))
}

func (p *parser) parse(c *Config) error {
	table := ""
	for ; p.i < len(p.lines); p.i++ {
		line := strings.TrimSpace(cutComment(p.lines[p.i]))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			switch name {
			case "tree", "tree.new", "tree.merge":
				table = name
			default:
				return p.errf("unknown table [%s]", name)
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return p.errf("a key = value line is needed")
		}
		full := strings.TrimSpace(key)
		if table != "" {
			full = table + "." + full
		}
		if p.seen[full] {
			return p.errf("%s is set twice", full)
		}
		p.seen[full] = true
		if err := p.set(c, full, strings.TrimSpace(value)); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) set(c *Config, key, value string) error {
	switch key {
	case "tree.remote":
		s, err := p.str(key, value)
		c.Tree.Remote = s
		return err
	case "tree.merge.checks":
		s, err := p.str(key, value)
		if err == nil && s != "auto" && s != "none" {
			return p.errf(`%s: "auto" or "none"`, key)
		}
		c.Tree.Merge.Checks = s
		return err
	case "tree.new.link":
		l, err := p.list(key, value)
		for _, s := range l {
			if clean := path.Clean(filepath.ToSlash(s)); clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || filepath.IsAbs(s) {
				return p.errf("%s: %q is not a path inside the checkout", key, s)
			}
		}
		c.Tree.New.Link = l
		return err
	case "tree.new.place":
		s, err := p.str(key, value)
		if err == nil && s != PlaceSibling && s != PlaceInside {
			return p.errf(`%s: "sibling" or "inside"`, key)
		}
		c.Tree.New.Place = s
		return err
	case "tree.new.run":
		l, err := p.list(key, value)
		c.Tree.New.Run = l
		return err
	case "tree.merge.require":
		l, err := p.list(key, value)
		c.Tree.Merge.Require = l
		return err
	}
	return p.errf("unknown key %s", key)
}

// str reads one quoted string.
func (p *parser) str(key, value string) (string, error) {
	s, rest, ok := quoted(value)
	if !ok || strings.TrimSpace(rest) != "" {
		return "", p.errf("%s: a quoted string is needed", key)
	}
	return s, nil
}

// list reads a list of quoted strings, which may go on over the next lines.
func (p *parser) list(key, value string) ([]string, error) {
	if !strings.HasPrefix(value, "[") {
		return nil, p.errf("%s: a list of quoted strings is needed", key)
	}
	rest := strings.TrimSpace(value[1:])
	start := p.i
	out := []string{}
	for {
		for rest == "" {
			p.i++
			if p.i >= len(p.lines) {
				p.i = start
				return nil, p.errf("%s: the list is not closed", key)
			}
			rest = strings.TrimSpace(cutComment(p.lines[p.i]))
		}
		if strings.HasPrefix(rest, "]") {
			if strings.TrimSpace(rest[1:]) != "" {
				return nil, p.errf("%s: text after the list", key)
			}
			return out, nil
		}
		s, after, ok := quoted(rest)
		if !ok {
			return nil, p.errf("%s: a list of quoted strings is needed", key)
		}
		out = append(out, s)
		rest = strings.TrimSpace(after)
		if strings.HasPrefix(rest, ",") {
			rest = strings.TrimSpace(rest[1:])
		} else if rest != "" && !strings.HasPrefix(rest, "]") {
			return nil, p.errf("%s: a comma is needed between the strings", key)
		}
	}
}

// quoted reads a "basic" or 'literal' string at the start of s and returns
// it and what follows.
func quoted(s string) (value, rest string, ok bool) {
	if s == "" {
		return "", "", false
	}
	switch s[0] {
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", "", false
		}
		return s[1 : 1+end], s[2+end:], true
	case '"':
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				i++
			case '"':
				v, err := strconv.Unquote(s[:i+1])
				if err != nil {
					return "", "", false
				}
				return v, s[i+1:], true
			}
		}
	}
	return "", "", false
}

// cutComment drops a # outside quotes and what follows it.
func cutComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return s[:i]
		}
	}
	return s
}
