package kernel

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEvent_TookMSRoundTrips(t *testing.T) {
	in := Event{Seq: 1, Kind: EvEdit, TookMS: 17}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"seq":1,"kind":"edit","tree":"","session":"","at":"0001-01-01T00:00:00Z","took_ms":17}`
	if string(raw) != want {
		t.Fatalf("marshal = %s, want %s", raw, want)
	}
	var out Event
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.TookMS != 17 {
		t.Fatalf("TookMS = %d, want 17", out.TookMS)
	}
}

func TestEvent_ZeroTookIsOmitted(t *testing.T) {
	raw, err := json.Marshal(Event{Seq: 2, Kind: EvCommit})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"seq":2,"kind":"commit","tree":"","session":"","at":"0001-01-01T00:00:00Z"}`
	if string(raw) != want {
		t.Fatalf("marshal = %s, want %s", raw, want)
	}
}

func TestEvent_LegacyLineReadsAsZeroTook(t *testing.T) {
	const line = `{"seq":3,"kind":"edit","tree":"t","session":"s","at":"2026-01-02T03:04:05Z"}`
	var ev Event
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.TookMS != 0 || ev.Seq != 3 || ev.Tree != "t" {
		t.Fatalf("legacy line read as %+v, want TookMS 0, seq 3, tree t", ev)
	}
}

func TestMillis_RoundsUpAndFloorsAtZero(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want int64
	}{
		{-5 * time.Millisecond, 0},
		{0, 0},
		{1, 1},
		{time.Millisecond, 1},
		{time.Millisecond + 1, 2},
		{11 * time.Millisecond, 11},
		{180*time.Millisecond - time.Nanosecond, 180},
	} {
		if got := Millis(c.d); got != c.want {
			t.Errorf("Millis(%v) = %d, want %d", c.d, got, c.want)
		}
	}
}
