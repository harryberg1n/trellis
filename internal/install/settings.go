// Package install wires trellis's hook entries into the user-level harness
// settings once per user, turns a repository on by its committed trellis.toml,
// removes exactly its own entries, and checks the result.
//
// Settings are edited as text. The file is scanned for byte offsets and only
// trellis's own hooks are spliced in or out, so every other byte (key order,
// spacing, number formatting such as 1.0, the byte order mark, the line
// endings) stays as the user wrote it. Nothing is decoded into a map and
// written back.
//
// Eject is the inverse of install without keeping any history. A container
// install had to create (the hooks object, an event's array) is written with
// whitespace right after its opening bracket; a container the user already had
// and install only filled gets the new text right after the bracket. Eject
// reads that difference: it removes a container that begins with whitespace
// once it is empty, and leaves any other empty container as it was found.
package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

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
	for i < len(doc) && isSpace(doc[i]) {
		i++
	}
	return i
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

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

// created reports whether install made this container: it begins with
// whitespace after its bracket. Only meaningful for a container that holds
// at least one item.
func created(doc []byte, c container) bool { return isSpace(doc[c.open+1]) }

// lineIndent is the leading whitespace of the line holding doc[pos].
func lineIndent(doc []byte, pos int) string {
	start := bytes.LastIndexByte(doc[:pos], '\n') + 1
	end := start
	for end < len(doc) && (doc[end] == ' ' || doc[end] == '\t') {
		end++
	}
	return string(doc[start:end])
}

// newline is the document's line ending, from its first line break.
func newline(doc []byte) string {
	i := bytes.IndexByte(doc, '\n')
	if i > 0 && doc[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// render encodes v: compact, or indented so that its continuation lines sit
// under ind. Line breaks are written as nl.
func render(v any, ind string, pretty bool, nl string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if pretty {
		enc.SetIndent(ind, "  ")
	}
	_ = enc.Encode(v) // the values are plain structs and slices
	out := strings.TrimSuffix(buf.String(), "\n")
	// Encoded strings escape their line breaks, so every raw one is structure.
	return strings.ReplaceAll(out, "\n", nl)
}

// builder produces the text of a new item for the indent and style of its
// neighbours.
type builder func(ind string, pretty bool, nl string) string

func memberOf(key string, v any) builder {
	return func(ind string, pretty bool, nl string) string {
		k := render(key, "", false, nl)
		if pretty {
			return k + ": " + render(v, ind, true, nl)
		}
		return k + ":" + render(v, "", false, nl)
	}
}

func elementOf(v any) builder {
	return func(ind string, pretty bool, nl string) string { return render(v, ind, pretty, nl) }
}

func splice(doc []byte, from, to int, text string) []byte {
	out := make([]byte, 0, len(doc)-(to-from)+len(text))
	out = append(out, doc[:from]...)
	out = append(out, text...)
	return append(out, doc[to:]...)
}

// appendItem adds an item after the last one, in the style of the last one.
//
// An empty container takes the document's style. When framed (a container
// install just made) the item sits on its own line, or after a space in a
// one-line document; otherwise the item goes right after the bracket and the
// user's own whitespace stays where it was.
func appendItem(doc []byte, c container, build builder, framed bool) []byte {
	nl := newline(doc)
	if len(c.items) == 0 {
		multi := bytes.IndexByte(doc, '\n') >= 0
		parent := lineIndent(doc, c.open)
		ind := parent + "  "
		switch {
		case !multi && framed:
			return splice(doc, c.open+1, c.close, " "+build("", false, nl))
		case !multi:
			return splice(doc, c.open+1, c.open+1, build("", false, nl))
		case framed:
			return splice(doc, c.open+1, c.close, nl+ind+build(ind, true, nl)+nl+parent)
		}
		return splice(doc, c.open+1, c.open+1, build(ind, true, nl))
	}
	last := c.items[len(c.items)-1]
	lead := string(doc[last.gap:last.start])
	pretty := strings.Contains(lead, "\n")
	ind := ""
	if pretty {
		ind = lead[strings.LastIndexByte(lead, '\n')+1:]
	}
	return splice(doc, last.val.end, last.val.end, ","+lead+build(ind, pretty, nl))
}

// removeItem takes item k out of a container that holds others, with its
// comma, undoing appendItem exactly.
func removeItem(doc []byte, c container, k int) []byte {
	if k == len(c.items)-1 {
		return splice(doc, c.items[k-1].val.end, c.items[k].val.end, "")
	}
	return splice(doc, c.items[k].start, c.items[k+1].start, "")
}

// dropSole deletes the only item of a container that was not framed, leaving
// the bracket pair and whatever whitespace it held.
func dropSole(doc []byte, c container) []byte {
	return splice(doc, c.items[0].start, c.items[0].val.end, "")
}

// prepare validates doc and returns it as JSON object text without a byte
// order mark. fresh says the file was empty and the object is made up here.
func prepare(doc []byte) (out []byte, bom, fresh bool, err error) {
	if rest, ok := bytes.CutPrefix(doc, utf8BOM); ok {
		doc, bom = rest, true
	}
	if len(bytes.TrimSpace(doc)) == 0 {
		return []byte("{}\n"), bom, true, nil
	}
	if !json.Valid(doc) {
		return nil, false, false, errors.New("settings are not valid JSON (comments are not supported)")
	}
	if doc[skipSpace(doc, 0)] != '{' {
		return nil, false, false, errors.New("settings are not a JSON object")
	}
	return doc, bom, false, nil
}

// finish checks an edited document and puts the byte order mark back. A
// splice that produced invalid JSON is a bug here, and the file is not
// written.
func finish(doc []byte, bom bool) ([]byte, error) {
	if !json.Valid(doc) {
		return nil, errors.New("internal error: the edit would produce invalid JSON, settings left untouched")
	}
	if bom {
		return append(append([]byte{}, utf8BOM...), doc...), nil
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

func hookAt(doc []byte, it item) Hook {
	var h Hook
	_ = json.Unmarshal(doc[it.val.start:it.val.end], &h)
	return h
}

// innerHooks is the "hooks" array of the entry object el, if it has one.
func innerHooks(doc []byte, el item) (container, bool) {
	if doc[el.val.start] != '{' {
		return container{}, false
	}
	obj := scan(doc, el.val.start)
	k := obj.find("hooks")
	if k < 0 || doc[obj.items[k].val.start] != '[' {
		return container{}, false
	}
	return scan(doc, obj.items[k].val.start), true
}

// firstOwnedHook finds the first owned hook in an event array: its entry, its
// place in the entry's hooks, and whether every hook of the entry is owned.
func firstOwnedHook(doc []byte, arr container, owned func(Hook) bool) (k, j int, inner container, whole bool) {
	for k, el := range arr.items {
		inner, ok := innerHooks(doc, el)
		if !ok {
			continue
		}
		j, whole := -1, true
		for jj, h := range inner.items {
			if owned(hookAt(doc, h)) {
				if j < 0 {
					j = jj
				}
			} else {
				whole = false
			}
		}
		if j >= 0 {
			return k, j, inner, whole
		}
	}
	return -1, -1, container{}, false
}

// addEntries puts each wanted entry under its event: it replaces the owned
// hook already there (the whole entry when every hook in it is owned), or
// appends an entry after the foreign ones. It returns one action line per
// event.
func addEntries(doc []byte, want []eventEntry, owned func(Hook) bool) ([]byte, []string, error) {
	doc, bom, fresh, err := prepare(doc)
	if err != nil {
		return nil, nil, err
	}
	var actions []string
	madeHooks := false
	for _, w := range want {
		hooks, ok, err := hooksOf(doc)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			doc = appendItem(doc, topOf(doc), memberOf("hooks", struct{}{}), fresh)
			madeHooks = true
			hooks, _, _ = hooksOf(doc)
		}
		ei := hooks.find(w.Event)
		if ei < 0 {
			doc = appendItem(doc, hooks, memberOf(w.Event, []Entry{}), madeHooks && len(hooks.items) == 0)
			hooks, _, _ = hooksOf(doc)
			ei = hooks.find(w.Event)
			doc = appendItem(doc, scan(doc, hooks.items[ei].val.start), elementOf(w.Entry), true)
			actions = append(actions, "add hook "+w.Event)
			continue
		}
		if doc[hooks.items[ei].val.start] != '[' {
			return nil, nil, fmt.Errorf("hooks.%s in the settings is not an array", w.Event)
		}
		var action string
		doc, action = setEntry(doc, scan(doc, hooks.items[ei].val.start), w, owned)
		actions = append(actions, action+" hook "+w.Event)
	}
	doc, err = finish(doc, bom)
	return doc, actions, err
}

// setEntry installs w into the event array arr and names what it did.
func setEntry(doc []byte, arr container, w eventEntry, owned func(Hook) bool) ([]byte, string) {
	k, j, inner, whole := firstOwnedHook(doc, arr, owned)
	switch {
	case k < 0:
		return appendItem(doc, arr, elementOf(w.Entry), false), "add"
	case whole:
		if reflect.DeepEqual(entryAt(doc, arr.items[k]), w.Entry) {
			return doc, "keep"
		}
		el := arr.items[k]
		pretty := bytes.Contains(doc[el.val.start:el.val.end], []byte("\n"))
		return splice(doc, el.val.start, el.val.end, render(w.Entry, lineIndent(doc, el.val.start), pretty, newline(doc))), "update"
	}
	h := inner.items[j]
	if hookAt(doc, h) == w.Entry.Hooks[0] {
		return doc, "keep"
	}
	pretty := bytes.Contains(doc[h.val.start:h.val.end], []byte("\n"))
	return splice(doc, h.val.start, h.val.end, render(w.Entry.Hooks[0], lineIndent(doc, h.val.start), pretty, newline(doc))), "update"
}

// removeEntries takes out every owned hook, and an entry once none of its
// hooks is left; then an event array and the hooks object, but only the ones
// install made. Everything else, empty containers included, stays as found.
func removeEntries(doc []byte, owned func(Hook) bool) ([]byte, []string, error) {
	doc, bom, _, err := prepare(doc)
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
			break
		}
		ei, k, j, inner := firstOwned(doc, hooks, owned)
		if ei < 0 {
			break
		}
		actions = append(actions, "remove hook "+hooks.items[ei].key)
		if len(inner.items) > 1 {
			doc = removeItem(doc, inner, j)
			continue
		}
		doc = removeEntry(doc, hooks, ei, k)
	}
	doc, err = finish(doc, bom)
	return doc, actions, err
}

func firstOwned(doc []byte, hooks container, owned func(Hook) bool) (ei, k, j int, inner container) {
	for ei, ev := range hooks.items {
		if doc[ev.val.start] != '[' {
			continue
		}
		if k, j, inner, _ := firstOwnedHook(doc, scan(doc, ev.val.start), owned); k >= 0 {
			return ei, k, j, inner
		}
	}
	return -1, -1, -1, container{}
}

// removeEntry takes entry k out of event ei, and the event and the hooks
// object after it when install made them and they are now empty.
func removeEntry(doc []byte, hooks container, ei, k int) []byte {
	arr := scan(doc, hooks.items[ei].val.start)
	switch {
	case len(arr.items) > 1:
		return removeItem(doc, arr, k)
	case !created(doc, arr):
		return dropSole(doc, arr)
	case len(hooks.items) > 1:
		return removeItem(doc, hooks, ei)
	case !created(doc, hooks):
		return dropSole(doc, hooks)
	}
	top := topOf(doc)
	hi := top.find("hooks")
	switch {
	case len(top.items) > 1:
		return removeItem(doc, top, hi)
	case !created(doc, top):
		return dropSole(doc, top)
	}
	return splice(doc, top.open+1, top.close, "")
}

// located is one entry found in a settings document.
type located struct {
	Event string
	Entry Entry
}

// walkEntries lists every entry under every event in document order.
func walkEntries(doc []byte) ([]located, error) {
	doc, _, _, err := prepare(doc)
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
