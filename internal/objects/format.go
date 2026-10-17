// Package objects writes git blobs and per-turn replay anchors without
// touching the user's index, branches or working tree.
package objects

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// commonDir is where a git dir keeps what it shares: its own, unless it has a
// commondir file (a linked worktree), which names the main git dir, relative
// to this one when not absolute.
func commonDir(gitDir string) string {
	raw, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	p := strings.TrimSpace(string(raw))
	if p == "" {
		return gitDir
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(gitDir, p)
	}
	return filepath.Clean(p)
}

// configValue is the last value of section.key in the config file of gitDir (or of
// its commondir), read without starting git. Section and key match without case.
func configValue(gitDir, section, key string) (string, bool, error) {
	f, err := os.Open(filepath.Join(commonDir(gitDir), "config"))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	in, found, value := false, false, ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			name, _, _ := strings.Cut(strings.TrimPrefix(line, "["), "]")
			in = strings.EqualFold(strings.TrimSpace(name), section)
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(stripComment(line), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), key) {
			value, found = strings.Trim(strings.TrimSpace(v), `"`), true
		}
	}
	return value, found, sc.Err()
}

// coreFilemode is git's core.filemode for the repo: the config value when it
// has one, else the platform default. With it off, a file's exec bit is not
// trusted.
func coreFilemode(gitDir string) bool {
	v, ok, err := configValue(gitDir, "core", "filemode")
	if err != nil || !ok {
		return filemodeDefault
	}
	switch strings.ToLower(v) {
	case "false", "no", "off", "0":
		return false
	case "true", "yes", "on", "1", "":
		return true
	}
	return filemodeDefault
}

// Format is the repo's object format, "sha1" or "sha256", read from the
// config file of gitDir (or of its commondir) without starting git. A repo
// that does not say is sha1.
func Format(gitDir string) (string, error) {
	f, err := os.Open(filepath.Join(commonDir(gitDir), "config"))
	if os.IsNotExist(err) {
		return "sha1", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	in := false
	value := "sha1"
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			name, _, _ := strings.Cut(strings.TrimPrefix(line, "["), "]")
			in = strings.EqualFold(strings.TrimSpace(name), "extensions")
			continue
		}
		if !in {
			continue
		}
		line = stripComment(line)
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "objectformat") {
			value = strings.ToLower(strings.Trim(strings.TrimSpace(v), `"`))
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	switch value {
	case "sha1", "sha256":
		return value, nil
	}
	return "", fmt.Errorf("objects: unknown extensions.objectformat %q", value)
}

func stripComment(s string) string {
	if i := strings.IndexAny(s, ";#"); i >= 0 {
		return s[:i]
	}
	return s
}
