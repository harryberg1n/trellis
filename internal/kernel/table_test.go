package kernel

import (
	"reflect"
	"testing"
)

func errStrings(errs []error) []string {
	if len(errs) == 0 {
		return nil
	}
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

func TestValidate_WallWithoutIncidentFails(t *testing.T) {
	got := errStrings(Validate([]Row{{ID: "w", Kind: Wall}}))
	want := []string{"w: wall row needs Incident"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors = %q, want %q", got, want)
	}
}

func TestValidate_PolicyWithoutDecisionFails(t *testing.T) {
	got := errStrings(Validate([]Row{{ID: "p", Kind: Policy}}))
	want := []string{"p: policy row needs Decision"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors = %q, want %q", got, want)
	}
}

func TestValidate_BlockingCheckWithoutPinFails(t *testing.T) {
	got := errStrings(Validate([]Row{{ID: "c", Kind: Check, Level: Block}}))
	want := []string{"c: block needs a may-block decision and an owner pin"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors = %q, want %q", got, want)
	}
}

func TestValidate_UndercoverPolicyPasses(t *testing.T) {
	if got := Validate([]Row{{ID: "undercover", Kind: Policy, Decision: "2026-09-01 owner"}}); len(got) != 0 {
		t.Errorf("errors = %v, want none", got)
	}
}

func TestValidate_BlockingCheckWithDecisionPasses(t *testing.T) {
	if got := Validate([]Row{{ID: "c", Kind: Check, Level: Block, Decision: "2026-09-01 owner"}}); len(got) != 0 {
		t.Errorf("errors = %v, want none", got)
	}
}

func TestValidate_DuplicateAndEmptyIDsFail(t *testing.T) {
	got := errStrings(Validate([]Row{{ID: "a"}, {ID: "a"}, {}}))
	want := []string{"a: duplicate row ID", "row 2: empty ID"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors = %q, want %q", got, want)
	}
}

func TestValidate_LiveABNeedsExperiment(t *testing.T) {
	got := errStrings(Validate([]Row{{ID: "l", State: LiveAB}}))
	want := []string{"l: live-ab row needs Experiment"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors = %q, want %q", got, want)
	}
}

func TestValidate_DecisionFormatIsChecked(t *testing.T) {
	cases := []struct {
		kind     Kind
		decision string
		want     []string
	}{
		{Policy, "d", []string{`p: Decision must be "<YYYY-MM-DD> <owner>", got "d"`}},
		{Policy, "2026-09-01 owner", nil},
		{Policy, "2026-13-01 owner", []string{`p: Decision must be "<YYYY-MM-DD> <owner>", got "2026-13-01 owner"`}},
		{Policy, "2026-09-01 ", []string{`p: Decision must be "<YYYY-MM-DD> <owner>", got "2026-09-01 "`}},
		{Policy, "2026-09-01 two owners", []string{`p: Decision must be "<YYYY-MM-DD> <owner>", got "2026-09-01 two owners"`}},
		{Check, "d", []string{`p: Decision must be "<YYYY-MM-DD> <owner>", got "d"`}},
		{Check, "2026-09-01 owner", nil},
	}
	for _, c := range cases {
		got := errStrings(Validate([]Row{{ID: "p", Kind: c.kind, Level: Block, Decision: c.decision}}))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("kind %d decision %q: errors = %q, want %q", c.kind, c.decision, got, c.want)
		}
	}
}

func TestValidate_ShippedRowsPass(t *testing.T) {
	if errs := Validate(Table); len(errs) != 0 {
		t.Errorf("Validate(Table) = %q, want no errors", errStrings(errs))
	}
}

func TestTable_ShipsTheTreeSixRows(t *testing.T) {
	got := make([]string, 0, len(Table))
	for _, r := range Table {
		got = append(got, r.ID)
	}
	want := []string{
		"primary-write", "main-commit", "hook-bypass", "discard-uncommitted", "own-git-state",
		"allow-people", "secrets", "undercover", "walls-warn-people", "secrets-everyone",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("row ids = %q, want %q", got, want)
	}
	kinds := map[string]Kind{"undercover": Policy, "walls-warn-people": Policy, "secrets-everyone": Policy, "secrets": Wall}
	for _, r := range Table {
		if k, ok := kinds[r.ID]; ok && r.Kind != k {
			t.Errorf("%s: kind %d, want %d", r.ID, r.Kind, k)
		}
		if r.State != Admitted {
			t.Errorf("%s: state %d, want Admitted", r.ID, r.State)
		}
	}
}

func TestTable_HookBypassIncident(t *testing.T) {
	for _, r := range Table {
		if r.ID == "hook-bypass" && r.Incident != "2026-09 bypass refusals (no-verify)" {
			t.Errorf("hook-bypass incident %q, want %q", r.Incident, "2026-09 bypass refusals (no-verify)")
		}
	}
}

func TestFind(t *testing.T) {
	rows := []Row{{ID: "a", Kind: Wall}, {ID: "b", Kind: Policy}}
	if r, ok := Find(rows, "b"); !ok || r.Kind != Policy {
		t.Errorf("Find(b) = %+v, %v; want the policy row", r, ok)
	}
	if r, ok := Find(rows, "c"); ok || r.ID != "" {
		t.Errorf("Find(c) = %+v, %v; want nothing", r, ok)
	}
}
