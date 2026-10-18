package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// Checkpoint is state.json: the lane arm vectors, the lane states and the
// last Seq folded into them.
type Checkpoint struct {
	LastSeq uint64                       `json:"last_seq"`
	Arms    map[string]map[string]string `json:"arms"`
	Lanes   map[string]kernel.LaneState  `json:"lanes"`
}

func (s *Store) statePath() string { return filepath.Join(s.dir, "state.json") }

// Checkpoint loads state.json; a missing file is an empty checkpoint. So is
// one that is empty or does not parse (a lazy Save can leave it so after a
// power loss): its bytes go to events.quarantine and the caller replays the
// log from Seq 1, as it does for any checkpoint behind the log.
func (tx *Tx) Checkpoint() (Checkpoint, error) {
	var cp Checkpoint
	raw, err := os.ReadFile(tx.s.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return cp, nil
	}
	if err != nil {
		return cp, err
	}
	if err := json.Unmarshal(raw, &cp); err != nil {
		tx.s.skip(logLine{off: -1, raw: raw})
		return Checkpoint{}, nil
	}
	return cp, nil
}

// Save writes state.json atomically: a temp file in the same directory,
// fsynced (unless the Tx is Lazy), then renamed over the old one. On any failure the old file stays
// as it was and the temp file is removed.
func (tx *Tx) Save(cp Checkpoint) error {
	raw, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(tx.s.dir, "state.json.*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(raw)
	if err == nil && tx.mode == Durable {
		err = tx.s.fsync(f)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = tx.s.rename(tmp, tx.s.statePath())
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// SetRename replaces the rename used to publish state.json. A test injects a
// failure through it; nil is not allowed.
func (s *Store) SetRename(f func(oldpath, newpath string) error) { s.rename = f }
