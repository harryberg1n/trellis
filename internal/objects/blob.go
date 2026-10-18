package objects

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// WriteBlob stores the file at path as a loose blob in gitDir's object store
// and returns its hex id. It starts no process: the object is
// "blob <len>\0" + content, hashed with the repo's format, deflated to
// objects/xx/rest through a temp file and a rename. An object already there is
// left alone.
//
// The object file is not fsynced: temp write plus rename only, like git's
// loose-object default before 2.36. A power loss can lose it; Stop re-anchors
// from disk, which rewrites whatever the turn's tree needs.
func WriteBlob(gitDir, path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return writeBlobContent(gitDir, content)
}

// writeBlobContent is WriteBlob for content already in hand.
func writeBlobContent(gitDir string, content []byte) (string, error) {
	format, err := Format(gitDir)
	if err != nil {
		return "", err
	}
	var h hash.Hash
	if format == "sha256" {
		h = sha256.New()
	} else {
		h = sha1.New()
	}
	header := "blob " + strconv.Itoa(len(content)) + "\x00"
	h.Write([]byte(header))
	h.Write(content)
	id := hex.EncodeToString(h.Sum(nil))

	dir := filepath.Join(commonDir(gitDir), "objects", id[:2])
	final := filepath.Join(dir, id[2:])
	if _, err := os.Stat(final); err == nil {
		return id, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var packed bytes.Buffer
	zw := zlib.NewWriter(&packed)
	if _, err := zw.Write([]byte(header)); err != nil {
		return "", err
	}
	if _, err := zw.Write(content); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "tmp_obj_")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(packed.Bytes())
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name()) // best effort: werr is the error worth reporting
		return "", werr
	}
	_ = os.Chmod(tmp.Name(), 0o444) // best effort, git's own loose-object mode
	if err := os.Rename(tmp.Name(), final); err != nil {
		_ = os.Remove(tmp.Name()) // best effort: the rename outcome is decided below
		// A concurrent writer may have won the rename; the object is then there.
		if _, serr := os.Stat(final); serr == nil {
			return id, nil
		} else if !errors.Is(serr, fs.ErrNotExist) {
			return "", serr
		}
		return "", fmt.Errorf("objects: storing %s: %w", id, err)
	}
	return id, nil
}
