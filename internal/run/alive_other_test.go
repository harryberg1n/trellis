//go:build !windows

package run

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// alive reports whether pid is still running; a zombie is dead.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return err != nil || !strings.Contains(string(stat), ") Z")
}
