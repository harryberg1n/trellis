//go:build !windows && !linux

// Copied from aphrollo-tools internal/run/pdeathsig_other.go at 12b3724e. See
// pdeathsig_linux.go for the incident (fix-bgwait-orphans).
package run

import "syscall"

// dieWithParent does nothing where the kernel has no parent-death signal
// (darwin, the BSDs): a SIGKILL of this process leaves the child running.
func dieWithParent(*syscall.SysProcAttr) {}
