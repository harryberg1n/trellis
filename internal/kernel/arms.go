package kernel

import (
	"crypto/sha256"
	"encoding/binary"
)

const (
	ArmTreatment = "treatment"
	ArmControl   = "control"
)

// Assign puts a lane in an experiment arm: the first 8 bytes of
// sha256(experiment + "\x00" + lane), read big-endian, mod 100, is the bucket;
// bucket < sharePct is treatment, else control.
func Assign(lane, experiment string, sharePct int) string {
	sum := sha256.Sum256([]byte(experiment + "\x00" + lane))
	bucket := binary.BigEndian.Uint64(sum[:8]) % 100
	if bucket < uint64(max(sharePct, 0)) {
		return ArmTreatment
	}
	return ArmControl
}

// Sticky keeps, per lane, the arm vector recorded at the lane's first event,
// so a share change never moves a lane already in flight.
type Sticky struct {
	vec map[string]map[string]string
}

func NewSticky() *Sticky { return &Sticky{vec: map[string]map[string]string{}} }

func copyArms(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Observe records the arms of ev that the lane has no arm for yet. Arms are
// sticky per experiment: a recorded arm is never overwritten.
func (s *Sticky) Observe(ev Event) {
	v := s.vec[ev.Lane]
	if v == nil {
		v = map[string]string{}
		s.vec[ev.Lane] = v
	}
	for exp, arm := range ev.Arms {
		if _, ok := v[exp]; !ok {
			v[exp] = arm
		}
	}
}

// Resolve returns the lane's arms. Every recorded arm is kept unchanged; an
// experiment in shares (experiment -> percent) with no recorded arm is
// assigned now and recorded, so it is sticky from then on.
func (s *Sticky) Resolve(lane string, shares map[string]int) map[string]string {
	v := s.vec[lane]
	if v == nil {
		v = map[string]string{}
		s.vec[lane] = v
	}
	for exp, pct := range shares {
		if _, ok := v[exp]; !ok {
			v[exp] = Assign(lane, exp, pct)
		}
	}
	return copyArms(v)
}
