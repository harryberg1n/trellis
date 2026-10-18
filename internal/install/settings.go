// Package install adds trellis's hook entries to a repo's harness settings,
// removes exactly those, and checks the result.
//
// Settings are edited as text. The file is scanned for byte offsets and only
// trellis's own entries are spliced in or out, so every other byte (key order,
// spacing, number formatting such as 1.0) stays as the user wrote it. Nothing
// is decoded into a map and written back.
package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Hook is one command hook in the harness settings.
type Hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// Entry is one element of an event's array: a matcher and its hooks.
type Entry struct {
	Matcher string `json:"matcher,omitempty"`
	Hooks   []Hook `json:"hooks"`
}

// eventEntry is the entry trellis wants under one event.
type eventEntry struct {
	Event string
	Entry Entry
}

// span is a half-open byte range of the document.
type span struct{ start, end int }

// item is one member of an object or element of an array.
type item struct {
	key   string // object members only
	gap   int    // offset just after the delimiter before this item
	start int    // offset of the key (members) or the value (elements)
	val   span
}

// container is a scanned object or array.
type container struct {
	open, close int // offsets of the brackets
	items       []item
}

// scan reads the object or array opening at doc[open]. doc must be valid JSON.
func scan(doc []byte, open int) container {
	closer := byte('}')
	if doc[open] == '[' {
		closer = ']'
	}
	c := container{open: open}
	gap := open + 1
	i := skipSpace(doc, gap)
	for doc[i] != closer {
		it := item{gap: gap, start: i}
		if closer == '}' {
			end := skipString(doc, i)
			_ = json.Unmarshal(doc[i:end], &it.key)
			i = skipSpace(doc, skipSpace(doc, end)+1)
		}
		it.val = span{i, skipValue(doc, i)}
		c.items = append(c.items, it)
		i = skipSpace(doc, it.val.end)
		if doc[i] == ',' {
			gap = i + 1
			i = skipSpace(doc, gap)
		}
	}
	c.close = i
	return c
}

func skipSpace(doc []byte, i int) int {
	for i < len(doc) && (doc[i] == ' ' || doc[i] == '\t' || doc[i] == '\n' || doc[i] == '\r') {
		i++
	}
	return i
}

func skipString(doc []byte, i int) int {
	for i++; doc[i] != '"'; i++ {
		if doc[i] == '\\' {
			i++
		}
	}
	return i + 1
}

func skipValue(doc []byte, i int) int {
	switch doc[i] {
	case '{', '[':
		return scan(doc, i).close + 1
	case '"':
		return skipString(doc, i)
	}
	for i < len(doc) && !strings.ContainsRune(",}] \t\r\n", rune(doc[i])) {
		i++
	}
	return i
}

func (c container) find(key string) int {
	for i, it := range c.items {
		if it.key == key {
			return i
		}
	}
	return -1
}

// lineIndent is the leading whitespace of the line holding doc[pos].
func lineIndent(doc []byte, pos int) string {
	start := bytes.LastIndexByte(doc[:pos], '\n') + 1
	end := start
	for end < len(doc) && (doc[end] == ' ' || doc[end] == '\t') {
		end++
	}
	return string(doc[start:end])
}

// render encodes v: compact, or indented so that its continuation lines sit
// under ind.
func render(v any, ind string, pretty bool) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if pretty {
		enc.SetIndent(ind, "  ")
	}
	_ = enc.Encode(v) // the values are plain structs and slices
	return strings.TrimSuffix(buf.String(), "\n")
}

// builder produces the text of a new item for the indent and style of its
// neighbours.
type builder func(ind string, pretty bool) string

func memberOf(key string, v any) builder {
	return func(ind string, pretty bool) string {
		k := render(key, "", false)
		if pretty {
			return k + ": " + render(v, ind, true)
		}
		return k + ":" + render(v, "", false)
	}
}

func elementOf(v any) builder {
	return func(ind string, pretty bool) string { return render(v, ind, pretty) }
}

func splice(doc []byte, from, to int, text string) []byte {
	out := make([]byte, 0, len(doc)-(to-from)+len(text))
	out = append(out, doc[:from]...)
	out = append(out, text...)
	return append(out, doc[to:]...)
}

// appendItem adds an item after the last one, in the style of the last one.
// An empty container takes the document's style.
func appendItem(doc []byte, c container, build builder) []byte {
	if len(c.items) == 0 {
		if !bytes.Contains(doc, []byte("\n")) {
			return splice(doc, c.open+1, c.close, build("", false))
		}
		parent := lineIndent(doc, c.open)
		ind := parent + "  "
		return splice(doc, c.open+1, c.close, "\n"+ind+build(ind, true)+"\n"+parent)
	}
	last := c.items[len(c.items)-1]
	lead := string(doc[last.gap:last.start])
	pretty := strings.Contains(lead, "\n")
	ind := ""
	if pretty {
		ind = lead[strings.LastIndexByte(lead, '\n')+1:]
	}
	return splice(doc, last.val.end, last.val.end, ","+lead+build(ind, pretty))
}

// removeItem takes item k out with its comma, undoing appendItem exactly.
func removeItem(doc []byte, c container, k int) []byte {
	switch {
	case len(c.items) == 1:
		return splice(doc, c.open+1, c.close, "")
	case k == len(c.items)-1:
		return splice(doc, c.items[k-1].val.end, c.items[k].val.end, "")
	}
	return splice(doc, c.items[k].start, c.items[k+1].start, "")
}

// prepare validates doc and returns it as a JSON object text. An empty file
// is an empty object.
func prepare(doc []byte) ([]byte, error) {
	if len(bytes.TrimSpace(doc)) == 0 {
		return []byte("{}\n"), nil
	}
	if !json.Valid(doc) {
		return nil, errors.New("settings are not valid JSON (comments and BOMs are not supported)")
	}
	if doc[skipSpace(doc, 0)] != '{' {
		return nil, errors.New("settings are not a JSON object")
	}
	return doc, nil
}

func topOf(doc []byte) container { return scan(doc, skipSpace(doc, 0)) }

// hooksOf returns the "hooks" object, or ok false when there is none.
func hooksOf(doc []byte) (c container, ok bool, err error) {
	top := topOf(doc)
	k := top.find("hooks")
	if k < 0 {
		return container{}, false, nil
	}
	if doc[top.items[k].val.start] != '{' {
		return container{}, false, errors.New(`"hooks" in the settings is not an object`)
	}
	return scan(doc, top.items[k].val.start), true, nil
}

func entryAt(doc []byte, it item) Entry {
	var e Entry
	_ = json.Unmarshal(doc[it.val.start:it.val.end], &e) // a foreign shape is just not ours
	return e
}

// addEntries puts each wanted entry under its event: it replaces the owned
// entry already there, or appends one after the foreign ones. It returns one
// action line per event.
func addEntries(doc []byte, want []eventEntry, owned func(Entry) bool) ([]byte, []string, error) {
	doc, err := prepare(doc)
	if err != nil {
		return nil, nil, err
	}
	var actions []string
	for _, w := range want {
		hooks, ok, err := hooksOf(doc)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			top := topOf(doc)
			doc = appendItem(doc, top, memberOf("hooks", struct{}{}))
			if hooks, _, err = hooksOf(doc); err != nil {
				return nil, nil, err
			}
		}
		ei := hooks.find(w.Event)
		if ei < 0 {
			doc = appendItem(doc, hooks, memberOf(w.Event, []Entry{w.Entry}))
			actions = append(actions, "add hook "+w.Event)
			continue
		}
		if doc[hooks.items[ei].val.start] != '[' {
			return nil, nil, fmt.Errorf("hooks.%s in the settings is not an array", w.Event)
		}
		arr := scan(doc, hooks.items[ei].val.start)
		found := -1
		for k, el := range arr.items {
			if owned(entryAt(doc, el)) {
				found = k
				break
			}
		}
		switch {
		case found < 0:
			doc = appendItem(doc, arr, elementOf(w.Entry))
			actions = append(actions, "add hook "+w.Event)
		case reflect.DeepEqual(entryAt(doc, arr.items[found]), w.Entry):
			actions = append(actions, "keep hook "+w.Event)
		default:
			el := arr.items[found]
			pretty := bytes.Contains(doc[el.val.start:el.val.end], []byte("\n"))
			doc = splice(doc, el.val.start, el.val.end, render(w.Entry, lineIndent(doc, el.val.start), pretty))
			actions = append(actions, "update hook "+w.Event)
		}
	}
	return doc, actions, nil
}

// removeEntries takes out every owned entry, then the event array, then the
// hooks object, for each that the removal left empty. Containers that were
// already empty, and everything foreign, are left as they are.
func removeEntries(doc []byte, owned func(Entry) bool) ([]byte, []string, error) {
	doc, err := prepare(doc)
	if err != nil {
		return nil, nil, err
	}
	var actions []string
	for {
		hooks, ok, err := hooksOf(doc)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return doc, actions, nil
		}
		ei, k := firstOwned(doc, hooks, owned)
		if ei < 0 {
			return doc, actions, nil
		}
		actions = append(actions, "remove hook "+hooks.items[ei].key)
		arr := scan(doc, hooks.items[ei].val.start)
		if len(arr.items) > 1 {
			doc = removeItem(doc, arr, k)
			continue
		}
		if len(hooks.items) > 1 {
			doc = removeItem(doc, hooks, ei)
			continue
		}
		top := topOf(doc)
		doc = removeItem(doc, top, top.find("hooks"))
	}
}

func firstOwned(doc []byte, hooks container, owned func(Entry) bool) (event, index int) {
	for ei, ev := range hooks.items {
		if doc[ev.val.start] != '[' {
			continue
		}
		for k, el := range scan(doc, ev.val.start).items {
			if owned(entryAt(doc, el)) {
				return ei, k
			}
		}
	}
	return -1, -1
}

// located is one entry found in a settings document.
type located struct {
	Event string
	Entry Entry
}

// walkEntries lists every entry under every event in document order.
func walkEntries(doc []byte) ([]located, error) {
	doc, err := prepare(doc)
	if err != nil {
		return nil, err
	}
	hooks, ok, err := hooksOf(doc)
	if err != nil || !ok {
		return nil, err
	}
	var out []located
	for _, ev := range hooks.items {
		if doc[ev.val.start] != '[' {
			continue
		}
		for _, el := range scan(doc, ev.val.start).items {
			out = append(out, located{Event: ev.key, Entry: entryAt(doc, el)})
		}
	}
	return out, nil
}
