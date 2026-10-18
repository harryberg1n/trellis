package kernel

import "testing"

func TestTree_EventAfterMergeIsFlagged(t *testing.T) {
	s := TreeNone
	s, ev := Advance(s, Event{Seq: 1, Kind: EvTreeOpened, Tree: "tree/x"})
	if s != TreeOpen {
		t.Fatalf("after first event state = %v, want open", s)
	}
	if _, ok := ev.Detail["after"]; ok {
		t.Errorf("event on an open tree was flagged: %v", ev.Detail)
	}
	s, _ = Advance(s, Event{Seq: 2, Kind: EvTreeMerged, Tree: "tree/x"})
	if s != TreeMerged {
		t.Fatalf("after tree.merged state = %v, want merged", s)
	}
	in := Event{Seq: 3, Kind: EvEdit, Tree: "tree/x", Detail: map[string]string{"file": "a.go"}}
	s, out := Advance(s, in)
	if s != TreeMerged {
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

func TestTree_AnyFirstEventOpensTheTree(t *testing.T) {
	s, _ := Advance(TreeNone, Event{Kind: EvCommit, Tree: "tree/y"})
	if s != TreeOpen {
		t.Errorf("state = %v, want open", s)
	}
}

// Abandonment is set by step 7's history fold, never by Advance. A tree that
// was abandoned and later merged is merged; step 7 relies on this.
func TestTree_MergedAfterAbandonedBecomesMerged(t *testing.T) {
	s, ev := Advance(TreeAbandoned, Event{Kind: EvTreeMerged, Tree: "tree/x"})
	if s != TreeMerged {
		t.Errorf("state = %v, want merged", s)
	}
	if _, ok := ev.Detail["after"]; ok {
		t.Errorf("tree.merged was flagged: %v", ev.Detail)
	}
}
