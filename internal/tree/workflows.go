package tree

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Issue #53 (fanpyp, 2026-11-04): the repository's two workflows run only on
// a push to main, its gate is the local --check, and every `tree merge` waited
// the whole checks window for a check no workflow could make: about 1 min 48 s
// per merge, 32 minutes in four days. The workflow files of the tree say which
// events start a run; when none of them can start one for this pull request,
// there is nothing to wait for.
//
// The reading is deliberately partial. Whatever it does not understand (a flow
// mapping, an anchor, a filter pattern beyond * and **, an event it does not
// know) counts as "can run": a wrong "cannot" merges before CI, a wrong "can"
// only costs the wait trellis had before.

// prSilentEvents are the events that never start a run for a pull request's
// head while trellis waits for its checks.
var prSilentEvents = []string{
	"branch_protection_rule", "check_run", "check_suite", "delete", "deployment",
	"deployment_status", "discussion", "discussion_comment", "fork", "gollum",
	"issue_comment", "issues", "label", "merge_group", "milestone", "page_build",
	"project", "project_card", "project_column", "public", "pull_request_review",
	"pull_request_review_comment", "registry_package", "release",
	"repository_dispatch", "schedule", "status", "watch", "workflow_call",
	"workflow_dispatch", "workflow_run",
}

// workflowsCanRun reports whether any workflow file of the checkout at root
// can start a run for a pull request from branch into base. files is false
// when the checkout holds no workflow file at all: then the answer is not the
// tree's to give, and the caller asks GitHub.
func workflowsCanRun(root, branch, base string) (files, canRun bool) {
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, false
	}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".yml" && ext != ".yaml") {
			continue
		}
		files = true
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || workflowCanRunOnPR(src, branch, base) {
			return true, true
		}
	}
	return files, false
}

// trigger is one event of a workflow's `on:` and its filters.
type trigger struct {
	event   string
	filters map[string][]string // branches, tags, types, paths, ...
}

// workflowCanRunOnPR reports whether the workflow src can start a run whose
// check lands on a pull request from branch into base.
func workflowCanRunOnPR(src []byte, branch, base string) bool {
	triggers, ok := parseOn(string(src))
	if !ok {
		return true
	}
	for _, tr := range triggers {
		if triggerCanRun(tr, branch, base) {
			return true
		}
	}
	return false
}

func triggerCanRun(tr trigger, branch, base string) bool {
	_, hasBranches := tr.filters["branches"]
	_, hasIgnore := tr.filters["branches-ignore"]
	switch tr.event {
	case "pull_request", "pull_request_target":
		if types, ok := tr.filters["types"]; ok && len(types) > 0 && allEqual(types, "closed") {
			return false
		}
		if hasIgnore || !hasBranches {
			return true
		}
		return anyMayMatch(tr.filters["branches"], base)
	case "push":
		_, hasTags := tr.filters["tags"]
		_, hasTagsIgnore := tr.filters["tags-ignore"]
		if !hasBranches && !hasIgnore {
			return !hasTags && !hasTagsIgnore // a tags filter alone leaves branch pushes out
		}
		if hasIgnore {
			return true
		}
		return anyMayMatch(tr.filters["branches"], branch)
	}
	return !slices.Contains(prSilentEvents, tr.event)
}

func allEqual(xs []string, want string) bool {
	for _, x := range xs {
		if x != want {
			return false
		}
	}
	return true
}

// anyMayMatch reports whether one of GitHub's filter patterns may match name.
// Only * (no slash) and ** are read; a pattern with ?, +, [ or a leading !
// may match anything.
func anyMayMatch(patterns []string, name string) bool {
	for _, p := range patterns {
		if strings.ContainsAny(p, "?+[]!\\") {
			return true
		}
		var re strings.Builder
		re.WriteString("^")
		for i := 0; i < len(p); i++ {
			switch {
			case strings.HasPrefix(p[i:], "**"):
				re.WriteString(".*")
				i++
			case p[i] == '*':
				re.WriteString("[^/]*")
			default:
				re.WriteString(regexp.QuoteMeta(p[i : i+1]))
			}
		}
		re.WriteString("$")
		if regexp.MustCompile(re.String()).MatchString(name) {
			return true
		}
	}
	return false
}

// yamlLine is a line of a workflow file with its comment cut off.
type yamlLine struct {
	indent int
	text   string // trimmed, never empty
}

func yamlLines(src string) []yamlLine {
	var out []yamlLine
	for _, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		line := cutComment(raw)
		text := strings.TrimSpace(line)
		if text == "" || text == "---" {
			continue
		}
		out = append(out, yamlLine{indent: len(line) - len(strings.TrimLeft(line, " ")), text: text})
	}
	return out
}

// cutComment drops a # that starts the line or follows a space, outside quotes.
func cutComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return s[:i]
		}
	}
	return s
}

// parseOn reads the top-level `on:` of a workflow. ok is false for anything
// it does not understand.
func parseOn(src string) ([]trigger, bool) {
	if strings.ContainsAny(src, "&*") && regexp.MustCompile(`(?m)(^|[\s:\[,])[&*][A-Za-z0-9_-]`).MatchString(src) {
		return nil, false // anchors and aliases
	}
	lines := yamlLines(src)
	start := -1
	var value string
	for i, l := range lines {
		if l.indent != 0 {
			continue
		}
		key, rest, found := strings.Cut(l.text, ":")
		if !found {
			continue
		}
		if k := unquote(strings.TrimSpace(key)); k == "on" || k == "true" {
			start, value = i, strings.TrimSpace(rest)
			break
		}
	}
	if start < 0 {
		return nil, false
	}
	if value != "" {
		if strings.HasPrefix(value, "{") {
			return nil, false
		}
		events, ok := flowList(value)
		if !ok {
			return nil, false
		}
		return eventsOnly(events), true
	}
	var block []yamlLine
	for _, l := range lines[start+1:] {
		if l.indent == 0 {
			break
		}
		block = append(block, l)
	}
	if len(block) == 0 {
		return nil, false
	}
	if strings.HasPrefix(block[0].text, "- ") {
		events, ok := blockList(block, block[0].indent)
		if !ok {
			return nil, false
		}
		return eventsOnly(events), true
	}
	return eventMap(block)
}

// eventMap reads `on:` as a mapping of events, each with its filters.
func eventMap(block []yamlLine) ([]trigger, bool) {
	depth := block[0].indent
	var out []trigger
	for i := 0; i < len(block); i++ {
		l := block[i]
		if l.indent < depth {
			return nil, false
		}
		if l.indent > depth {
			continue // read by the event above it
		}
		key, rest, found := strings.Cut(l.text, ":")
		if !found || strings.HasPrefix(key, "<<") {
			return nil, false
		}
		tr := trigger{event: unquote(strings.TrimSpace(key)), filters: map[string][]string{}}
		if v := strings.TrimSpace(rest); v != "" && v != "null" && v != "~" {
			return nil, false // an inline mapping or a value trellis does not read
		}
		var body []yamlLine
		for _, b := range block[i+1:] {
			if b.indent <= depth {
				break
			}
			body = append(body, b)
		}
		if len(body) > 0 && tr.event != "schedule" && tr.event != "workflow_dispatch" && tr.event != "workflow_call" {
			if !readFilters(body, tr.filters) {
				return nil, false
			}
		}
		out = append(out, tr)
	}
	return out, true
}

// readFilters reads an event's filters: each a key with a flow list or a
// block list of values.
func readFilters(body []yamlLine, filters map[string][]string) bool {
	depth := body[0].indent
	for i := 0; i < len(body); i++ {
		l := body[i]
		if l.indent > depth {
			continue
		}
		if l.indent < depth {
			return false
		}
		key, rest, found := strings.Cut(l.text, ":")
		if !found {
			return false
		}
		key = unquote(strings.TrimSpace(key))
		if v := strings.TrimSpace(rest); v != "" {
			values, ok := flowList(v)
			if !ok {
				return false
			}
			filters[key] = values
			continue
		}
		var items []yamlLine
		for _, b := range body[i+1:] {
			if b.indent <= depth {
				break
			}
			items = append(items, b)
		}
		if len(items) == 0 {
			filters[key] = nil
			continue
		}
		values, ok := blockList(items, items[0].indent)
		if !ok {
			return false
		}
		filters[key] = values
	}
	return true
}

// flowList reads `[a, 'b']` or a single scalar.
func flowList(v string) ([]string, bool) {
	if !strings.HasPrefix(v, "[") {
		if strings.ContainsAny(v, "{}[]") {
			return nil, false
		}
		return []string{unquote(v)}, true
	}
	if !strings.HasSuffix(v, "]") || strings.ContainsAny(v[1:len(v)-1], "{}[]") {
		return nil, false
	}
	var out []string
	for _, item := range strings.Split(v[1:len(v)-1], ",") {
		if item = unquote(strings.TrimSpace(item)); item != "" {
			out = append(out, item)
		}
	}
	return out, true
}

// blockList reads `- a` items at depth.
func blockList(items []yamlLine, depth int) ([]string, bool) {
	var out []string
	for _, l := range items {
		if l.indent != depth || !strings.HasPrefix(l.text, "- ") {
			return nil, false
		}
		item := strings.TrimSpace(strings.TrimPrefix(l.text, "- "))
		if strings.ContainsAny(item, "{}[]:") {
			return nil, false
		}
		out = append(out, unquote(item))
	}
	return out, true
}

func eventsOnly(events []string) []trigger {
	out := make([]trigger, 0, len(events))
	for _, e := range events {
		out = append(out, trigger{event: e, filters: map[string][]string{}})
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
