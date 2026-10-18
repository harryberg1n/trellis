// Source: aphrollo-tools 12b3724e internal/tdd/precommit/secretscan.go
// (gitleaksVersion, secretScanVersionCache). Incident: the commit gate asked
// `gitleaks version` on every commit; the answer is kept under the binary's
// path and mtime, so a reinstall asks again and a hook spawns no second process.

package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// toolVersionFile is the small file beside the state root's repos that holds
// the version each external tool reported.
const toolVersionFile = "tool-versions.json"

// toolVersion is what the file keeps of one `<tool> version`.
type toolVersion struct {
	MTime   int64  `json:"mtime"`
	Version string `json:"version"`
}

func readToolVersions(root string) map[string]toolVersion {
	raw, err := readFileShared(filepath.Join(root, toolVersionFile))
	if err != nil {
		return nil
	}
	var m map[string]toolVersion
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// LoadToolVersion is the version saved for the binary at bin when its mtime
// was mtime, and false when none was or the binary has changed since.
func LoadToolVersion(root, bin string, mtime time.Time) (string, bool) {
	v, ok := readToolVersions(root)[bin]
	if !ok || v.MTime != mtime.UnixNano() {
		return "", false
	}
	return v.Version, true
}

// SaveToolVersion keeps version for the binary at bin as it was at mtime. A
// cache that cannot be read is replaced.
func SaveToolVersion(root, bin string, mtime time.Time, version string) error {
	m := readToolVersions(root)
	if m == nil {
		m = map[string]toolVersion{}
	}
	m[bin] = toolVersion{MTime: mtime.UnixNano(), Version: version}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, dirMode); err != nil {
		return err
	}
	return WriteAtomic(root, toolVersionFile, raw, WithMode(fileMode))
}
