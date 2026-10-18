package update

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// errNotWritable is what replaceExe reports when it could not even stage the
// new binary beside the old one: nothing was changed.
var errNotWritable = errors.New("update: the directory of the binary is not writable")

// notWritable is err, marked errNotWritable when it is a refusal of permission
// or a read-only file system; any other failure (a full disk, a missing
// directory) stays what it was.
func notWritable(err error) error {
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS) {
		return fmt.Errorf("%w: %w", errNotWritable, err)
	}
	return err
}

// oldSuffix names the running binary once Windows has moved it aside.
const oldSuffix = ".old"

// removeStale deletes the `.old` copy a previous update moved aside, best
// effort: a copy still running cannot be deleted, and the next run tries again.
func removeStale(exe string) {
	if exe != "" {
		_ = os.Remove(exe + oldSuffix)
	}
}
