package measure

import (
	"reflect"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
)

func TestSpeed_P50P90PerWeek(t *testing.T) {
	// 2026-10-05 is a Monday: ISO week 2026-W41.
	mon := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	evs := make([]kernel.Event, 0, 11)
	ms := make([]Merge, 0, 11)
	for i, mins := range []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100} {
		lane := string(rune('a' + i))
		evs = append(evs, kernel.Event{Kind: kernel.EvLaneOpened, Lane: lane, At: mon})
		ms = append(ms, Merge{Lane: lane, PR: i + 1, At: mon.Add(time.Duration(mins) * time.Minute)})
	}
	// A lane merged the next week, 5 hours after it opened.
	evs = append(evs, kernel.Event{Kind: kernel.EvLaneOpened, Lane: "z", At: mon.Add(7 * 24 * time.Hour)})
	ms = append(ms, Merge{Lane: "z", PR: 99, At: mon.Add(7*24*time.Hour + 5*time.Hour)})

	got := Speed(Facts{Events: evs, Merges: ms})

	want := []WeekSpeed{
		{Week: "2026-W41", N: 10, P50: 50 * time.Minute, P90: 90 * time.Minute},
		{Week: "2026-W42", N: 1, P50: 5 * time.Hour, P90: 5 * time.Hour},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("speed = %+v, want %+v", got, want)
	}
}

func TestSpeed_TwoMergesOnOneLaneGetTheirOwnLeadTimes(t *testing.T) {
	// Lane L opened Monday 2026-10-05 08:00 (W41) and merged 60 minutes later;
	// it opened again on Monday 10-12 (W42) and merged 30 minutes after that.
	// The log lists the newest merge first.
	t1 := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	f := Facts{
		Events: []kernel.Event{
			{Kind: kernel.EvLaneOpened, Lane: "L", At: t1},
			{Kind: kernel.EvLaneOpened, Lane: "L", At: t2},
		},
		Merges: []Merge{
			{Sha: "bbbbbbb2222", Lane: "L", PR: 2, At: t2.Add(30 * time.Minute)},
			{Sha: "aaaaaaa1111", Lane: "L", PR: 1, At: t1.Add(60 * time.Minute)},
		},
	}

	got := Speed(f)

	want := []WeekSpeed{
		{Week: "2026-W41", N: 1, P50: 60 * time.Minute, P90: 60 * time.Minute},
		{Week: "2026-W42", N: 1, P50: 30 * time.Minute, P90: 30 * time.Minute},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("speed = %+v, want %+v", got, want)
	}
}

func TestSpeed_LaneWithoutOpenEventIsSkipped(t *testing.T) {
	got := Speed(Facts{Merges: []Merge{{Lane: "x", PR: 1, At: day(0)}}})
	if len(got) != 0 {
		t.Errorf("speed = %+v, want none", got)
	}
}
