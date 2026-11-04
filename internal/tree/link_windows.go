package tree

import (
	"os"
	"time"

	"github.com/harryberg1n/trellis/internal/run"
)

// makeLink makes link a symbolic link to target. Windows lets only an
// elevated process or Developer Mode make one; a directory then gets a
// junction, which needs neither.
func makeLink(env Env, target, link string) error {
	err := os.Symlink(target, link)
	if err == nil {
		return nil
	}
	if info, serr := os.Stat(target); serr != nil || !info.IsDir() {
		return err
	}
	_, jerr := env.runner().Light(run.Spec{Name: "cmd", Args: []string{"/C", "mklink", "/J", link, target}, Timeout: 30 * time.Second})
	return jerr
}
