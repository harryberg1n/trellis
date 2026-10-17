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

// Observe records ev.Arms as the lane's vector if it is the lane's first.
func (s *Sticky) Observe(ev Event) {
	if _, ok := s.vec[ev.Lane]; !ok {
		s.vec[ev.Lane] = copyArms(ev.Arms)
	}
}

// Resolve returns the lane's recorded vector, or assigns one from the current
// shares (experiment -> percent) and records it.
func (s *Sticky) Resolve(lane string, shares map[string]int) map[string]string {
	if v, ok := s.vec[lane]; ok {
		return copyArms(v)
	}
	v := make(map[string]string, len(shares))
	for exp, pct := range shares {
		v[exp] = Assign(lane, exp, pct)
	}
	s.vec[lane] = v
	return copyArms(v)
}
