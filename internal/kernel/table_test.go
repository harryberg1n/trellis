package kernel

import (
	"reflect"
	"testing"
)

func errStrings(errs []error) []string {
	var out []string
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
