package store

import (
	"encoding/json"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// This file exists for records written by v0.0.1, which called a tree a lane:
// the JSON key "lane", the kinds "lane.opened" and "lane.merged", and the
// checkpoint key "lanes". On read they are accepted as the tree spellings.
// Nothing writes the old spellings. It may be removed after 2026-11-18.

// currentKind maps a v0.0.1 event kind to its tree kind; others pass through.
func currentKind(kind string) string {
	switch kind {
	case "lane.opened":
		return kernel.EvTreeOpened
	case "lane.merged":
		return kernel.EvTreeMerged
	}
	return kind
}

// decodeEvent parses one event record, v0.0.1 spellings included.
func decodeEvent(raw []byte, ev *kernel.Event) error {
	var old struct {
		Lane string `json:"lane"`
	}
	if err := json.Unmarshal(raw, ev); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		return err
	}
	if ev.Tree == "" {
		ev.Tree = old.Lane
	}
	ev.Kind = currentKind(ev.Kind)
	return nil
}

// decodeCheckpoint parses state.json, a v0.0.1 "lanes" key included.
func decodeCheckpoint(raw []byte, cp *Checkpoint) error {
	var old struct {
		Lanes map[string]kernel.TreeState `json:"lanes"`
	}
	if err := json.Unmarshal(raw, cp); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		return err
	}
	if len(cp.Trees) == 0 {
		cp.Trees = old.Lanes
	}
	return nil
}
