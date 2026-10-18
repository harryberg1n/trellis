package update

import (
	"errors"
	"fmt"
	"os"
)

// errNotWritable is what replaceExe reports when it could not even stage the
// new binary beside the old one: nothing was changed.
var errNotWritable = errors.New("update: the directory of the binary is not writable")

func notWritable(err error) error { return fmt.Errorf("%w: %w", errNotWritable, err) }

// oldSuffix names the running binary once Windows has moved it aside.
const oldSuffix = ".old"

// removeStale deletes the `.old` copy a previous update moved aside, best
// effort: a copy still running cannot be deleted, and the next run tries again.
func removeStale(exe string) {
	if exe != "" {
		_ = os.Remove(exe + oldSuffix)
	}
}
