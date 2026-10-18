package measure

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// price is a model's list price in USD per million tokens.
//
// Copied from aphrollo-tools internal/report/usage.go:13-50 at 12b3724e.
// Incident: a price change must be one line, and the cost of a group is
// computed from the tokens when read, never stored.
type price struct{ Input, Output, CacheRead, CacheWrite float64 }

// modelPrices are list prices by model-id prefix, so a dated or [1m] id of a
// family resolves; the longest matching prefix wins. A notional cost, not an
// invoice: sessions may ride a flat plan.
var modelPrices = []struct {
	prefix string
	price  price
}{
	{"claude-fable-5", price{10, 50, 1, 12.5}},
	{"claude-opus-5", price{5, 25, 0.5, 6.25}},
	{"claude-opus-4-8", price{5, 25, 0.5, 6.25}},
	{"claude-sonnet-5", price{3, 15, 0.3, 3.75}},
	{"claude-sonnet-4-6", price{3, 15, 0.3, 3.75}},
	{"claude-haiku-4-5", price{1, 5, 0.1, 1.25}},
}

// atFamily reports whether model is the family prefix, or the prefix followed
// by "-" or "[": "claude-opus-5-5" and "claude-opus-5[1m]" are family
// members, "claude-opus-5x" is not.
func atFamily(model, prefix string) bool {
	rest, ok := strings.CutPrefix(model, prefix)
	return ok && (rest == "" || rest[0] == '-' || rest[0] == '[')
}

func priceFor(model string) (price, bool) {
	best, ok := price{}, false
	n := 0
	for _, m := range modelPrices {
		if atFamily(model, m.prefix) && len(m.prefix) > n {
			best, ok, n = m.price, true, len(m.prefix)
		}
	}
	return best, ok
}

// tokens are the counts of one reply.
type tokens struct{ Fresh, CacheWrite, CacheWrite1h, CacheRead, Output int64 }

// costUSD prices tokens; a model with no price costs 0.
func costUSD(model string, t tokens) float64 {
	p, ok := priceFor(model)
	if !ok {
		return 0
	}
	// The 1-hour cache write costs twice the input price, the 5-minute one 1.25 times.
	w5m, w1h := t.CacheWrite-t.CacheWrite1h, t.CacheWrite1h
	return (float64(t.Fresh)*p.Input + float64(t.Output)*p.Output + float64(t.CacheRead)*p.CacheRead + float64(w5m)*p.CacheWrite + float64(w1h)*2*p.Input) / 1e6
}

// turn is one model reply of a transcript.
type turn struct {
	at    time.Time
	model string
	tok   tokens
}

// maxLineBytes bounds one transcript line; a longer one is skipped.
const maxLineBytes = 8 << 20

// syntheticModel marks a record the harness wrote itself; it is not a reply.
const syntheticModel = "<synthetic>"

// transcriptLine is the part of a record read; unknown fields are ignored.
//
// Parsing copied from aphrollo-tools internal/report/usage_scan.go at
// 12b3724e. Incident: a reply split over several records shares a requestId
// and must be counted once, by its record with the most output tokens.
type transcriptLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	RequestID string `json:"requestId"`
	Message   *struct {
		Model string `json:"model"`
		Usage *struct {
			Input         int64 `json:"input_tokens"`
			CCreate       int64 `json:"cache_creation_input_tokens"`
			CRead         int64 `json:"cache_read_input_tokens"`
			Output        int64 `json:"output_tokens"`
			CacheCreation *struct {
				Hour int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// readLine is the next line without its newline. A line past maxLineBytes is
// read through and dropped (tooLong), so memory stays bounded and the lines
// after it still count.
//
// Copied from aphrollo-tools internal/report/usage_scan.go:212-231 at
// 12b3724e. Incident: bufio.Scanner stops for good on an over-long line.
func readLine(r *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		part, isPrefix, e := r.ReadLine()
		if e != nil {
			return line, tooLong, e
		}
		if !tooLong {
			if len(line)+len(part) > maxLineBytes {
				tooLong, line = true, nil
			} else {
				line = append(line, part...)
			}
		}
		if !isPrefix {
			return line, tooLong, nil
		}
	}
}

// parseTurns reads the replies of a JSONL transcript, once per requestId, in
// time order. A line that is not a reply is skipped; no text of the
// transcript is kept.
func parseTurns(raw []byte) []turn {
	byReq := map[string]turn{}
	var order []string
	var loose []turn
	r := bufio.NewReader(bytes.NewReader(raw))
	for {
		line, tooLong, err := readLine(r)
		if err != nil {
			break
		}
		if tooLong {
			continue
		}
		var l transcriptLine
		if json.Unmarshal(line, &l) != nil || l.Type != "assistant" || l.Message == nil || l.Message.Usage == nil || l.Message.Model == syntheticModel {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
		if err != nil {
			continue
		}
		u := l.Message.Usage
		t := turn{at: at, model: l.Message.Model, tok: tokens{Fresh: u.Input, CacheWrite: u.CCreate, CacheRead: u.CRead, Output: u.Output}}
		if u.CacheCreation != nil {
			t.tok.CacheWrite1h = u.CacheCreation.Hour
		}
		if l.RequestID == "" {
			loose = append(loose, t)
			continue
		}
		prev, seen := byReq[l.RequestID]
		if !seen {
			order = append(order, l.RequestID)
		}
		if !seen || t.tok.Output > prev.tok.Output {
			// The turn happened when its reply began.
			if seen {
				t.at = prev.at
			}
			byReq[l.RequestID] = t
		}
	}
	out := loose
	for _, id := range order {
		out = append(out, byReq[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out
}

// treeOf is the tree a reply at t belongs to in usage u.
func (u Usage) treeOf(t time.Time) string {
	for _, m := range u.Marks {
		if !m.At.Before(t) {
			return m.Tree
		}
	}
	if n := len(u.Marks); n > 0 {
		return u.Marks[n-1].Tree
	}
	return u.Tree
}

// marked is a reply with the tree it is charged to.
type marked struct {
	turn
	tree string
}

// turns are the replies of the usage's transcript, each with its tree.
func (u Usage) turns() []marked {
	ts := parseTurns(u.Transcript)
	out := make([]marked, len(ts))
	for i, t := range ts {
		out[i] = marked{t, u.treeOf(t.at)}
	}
	return out
}

// Unpriced counts the tokens whose model has no price, so a cost of 0 for them
// is not read as free.
type Unpriced struct {
	Tokens int64            // input and output tokens, all trees
	ByTree map[string]int64 // the same per tree
	Models []string         // the models with no price, sorted
}

// TreeCosts is the agent dollars per tree at list price, from the transcripts.
func TreeCosts(f Facts) map[string]float64 {
	costs, _ := Costs(f)
	return costs
}

// Costs is TreeCosts and the tokens it could not price.
func Costs(f Facts) (map[string]float64, Unpriced) {
	out := map[string]float64{}
	un := Unpriced{ByTree: map[string]int64{}}
	models := map[string]bool{}
	for _, u := range f.Usage {
		for _, t := range u.turns() {
			if _, ok := priceFor(t.model); !ok {
				n := t.tok.Fresh + t.tok.Output
				un.Tokens += n
				un.ByTree[t.tree] += n
				models[t.model] = true
				continue
			}
			out[t.tree] += costUSD(t.model, t.tok)
		}
	}
	un.Models = sortedKeys(models)
	return out, un
}

// RowCost is the net_cost inputs of one table row: what its denies injected
// and the turns the agent spent before the next green.
type RowCost struct {
	Row             string
	Denies          int
	InjectedTokens  int64 // injected bytes / 4
	Turns           int
	Unresolved      int // denies with no green yet
	UnresolvedTurns int // turns after those denies, capped; not in Turns
}

// RowCosts folds the wall.denied events into per-row net_cost inputs, by row
// id.
//
// A deny is a wall.denied event; its row is Detail["row"] and the line it
// injected is Detail["bytes"] bytes long (else the length of Detail["line"]).
// Its green is the first later event of the same tree with kind ci.result and
// Detail["result"] == "green". The turns it charges are the agent replies of
// that tree strictly after the deny and not after the green, or after the deny
// to the end of the transcript when no green came (then it counts as Unresolved).
// A reply is an assistant message of the tree's transcripts, once per
// requestId; a tree with no transcript uses its turn.anchored events instead.
func RowCosts(f Facts) []RowCost {
	times := map[string][]time.Time{}
	for _, u := range f.Usage {
		for _, t := range u.turns() {
			times[t.tree] = append(times[t.tree], t.at)
		}
	}
	withUsage := map[string]bool{}
	for tree, ts := range times {
		withUsage[tree] = len(ts) > 0
	}
	for _, ev := range f.Events {
		if ev.Kind == kernel.EvTurnAnchored && !withUsage[ev.Tree] {
			times[ev.Tree] = append(times[ev.Tree], ev.At)
		}
	}
	rows := map[string]*RowCost{}
	merges := linked(f)
	bytesBy := map[string]int64{}
	for _, d := range f.Events {
		if d.Kind != kernel.EvWallDenied {
			continue
		}
		id := d.Detail[kernel.DetailRow]
		rc := rows[id]
		if rc == nil {
			rc = &RowCost{Row: id}
			rows[id] = rc
		}
		rc.Denies++
		if n, err := strconv.ParseInt(d.Detail[kernel.DetailBytes], 10, 64); err == nil {
			bytesBy[id] += n
		} else {
			bytesBy[id] += int64(len(d.Detail[kernel.DetailLine]))
		}
		green, resolved := nextGreen(f.Events, d)
		if !resolved {
			rc.Unresolved++
		}
		end, inclusive, capped := capFor(f.Events, merges, d)
		n := 0
		for _, at := range times[d.Tree] {
			switch {
			case !at.After(d.At):
			case resolved:
				if !at.After(green) {
					rc.Turns++
				}
			case n < maxUnresolvedTurns && (!capped || at.Before(end) || (inclusive && at.Equal(end))):
				n++
			}
		}
		rc.UnresolvedTurns += n
	}
	out := make([]RowCost, 0, len(rows))
	for _, id := range sortedKeys(rows) {
		rc := *rows[id]
		rc.InjectedTokens = bytesBy[id] / 4
		out = append(out, rc)
	}
	return out
}

// maxUnresolvedTurns caps the turns charged to a deny that has no green.
const maxUnresolvedTurns = 50

// capFor is where the turns of an unresolved deny end: at the tree's next
// deny (exclusive) or at the tree's merge (inclusive), whichever is first.
// ok is false when neither exists; the turn count cap still applies.
func capFor(evs []kernel.Event, merges []Merge, deny kernel.Event) (end time.Time, inclusive, ok bool) {
	for _, ev := range evs {
		if ev.Kind == kernel.EvWallDenied && ev.Tree == deny.Tree && ev.At.After(deny.At) && (!ok || ev.At.Before(end)) {
			end, inclusive, ok = ev.At, false, true
		}
	}
	for _, m := range merges {
		if m.Tree == deny.Tree && !m.At.Before(deny.At) && (!ok || m.At.Before(end)) {
			end, inclusive, ok = m.At, true, true
		}
	}
	return end, inclusive, ok
}

func nextGreen(evs []kernel.Event, deny kernel.Event) (time.Time, bool) {
	var best time.Time
	found := false
	for _, ev := range evs {
		if ev.Kind == kernel.EvCIResult && ev.Tree == deny.Tree && ev.Detail[kernel.DetailResult] == "green" && ev.At.After(deny.At) && (!found || ev.At.Before(best)) {
			best, found = ev.At, true
		}
	}
	return best, found
}
