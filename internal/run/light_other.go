//go:build !windows

// Copied from aphrollo-tools internal/run/tree_unix.go and
// internal/proc/killtree_unix.go at 12b3724e. Incident: #997, a timed-out
// child's descendants kept running because only the child was killed. The
// child leads its own process group and a signal to the group reaches the tree.
package run

import (
	"os"
	"os/exec"
	"syscall"
)

// groupTree guards a child through its own process group.
type groupTree struct{ pid int }

func prepare(cmd *exec.Cmd) (tree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &groupTree{}, nil
}

func (g *groupTree) attach(p *os.Process) error {
	g.pid = p.Pid
	return nil
}

// kill signals the group; a group with no process left answers ESRCH, which
// is the goal. A tree never attached has no group, and pid 0 would be this
// process's own.
func (g *groupTree) kill() {
	if g.pid > 0 {
		_ = syscall.Kill(-g.pid, syscall.SIGKILL)
	}
}

func (g *groupTree) finish() {}
