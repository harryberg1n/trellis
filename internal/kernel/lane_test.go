package kernel

import "testing"

func TestLane_EventAfterMergeIsFlagged(t *testing.T) {
	s := LaneNone
	s, ev := Advance(s, Event{Seq: 1, Kind: EvLaneOpened, Lane: "lane/x"})
	if s != LaneOpen {
		t.Fatalf("after first event state = %v, want open", s)
	}
	if _, ok := ev.Detail["after"]; ok {
		t.Errorf("event on an open lane was flagged: %v", ev.Detail)
	}
	s, _ = Advance(s, Event{Seq: 2, Kind: EvLaneMerged, Lane: "lane/x"})
	if s != LaneMerged {
		t.Fatalf("after lane.merged state = %v, want merged", s)
	}
	in := Event{Seq: 3, Kind: EvEdit, Lane: "lane/x", Detail: map[string]string{"file": "a.go"}}
	s, out := Advance(s, in)
	if s != LaneMerged {
		t.Errorf("state after late event = %v, want merged", s)
	}
	if out.Kind != EvEdit || out.Seq != 3 {
		t.Errorf("late event not kept: %+v", out)
	}
	if out.Detail["after"] != "merged" || out.Detail["file"] != "a.go" {
		t.Errorf("Detail = %v, want after=merged and file=a.go", out.Detail)
	}
	if _, ok := in.Detail["after"]; ok {
		t.Error("Advance mutated the caller's Detail map")
	}
}

func TestLane_AnyFirstEventOpensTheLane(t *testing.T) {
	s, _ := Advance(LaneNone, Event{Kind: EvCommit, Lane: "lane/y"})
	if s != LaneOpen {
		t.Errorf("state = %v, want open", s)
	}
}

// Abandonment is set by lane 7's history fold, never by Advance. A lane that
// was abandoned and later merged is merged; lane 7 relies on this.
func TestLane_MergedAfterAbandonedBecomesMerged(t *testing.T) {
	s, ev := Advance(LaneAbandoned, Event{Kind: EvLaneMerged, Lane: "lane/x"})
	if s != LaneMerged {
		t.Errorf("state = %v, want merged", s)
	}
	if _, ok := ev.Detail["after"]; ok {
		t.Errorf("lane.merged was flagged: %v", ev.Detail)
	}
}
