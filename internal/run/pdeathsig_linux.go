//go:build linux

// Copied from aphrollo-tools internal/run/pdeathsig_linux.go at 12b3724e.
// Incident: fix-bgwait-orphans, a child outlived the process that started it
// when that process was killed, because only a signal this process could catch
// reached the child.
package run

import "syscall"

// dieWithParent has the kernel kill the child when the process that started
// it dies, however it dies: a SIGKILL, a crash or an out-of-memory kill no
// handler of ours sees. It reaches the child itself, not what the child
// started; the child's process group is ended by our own kill and terminate.
//
// Pdeathsig fires when the thread that started the child exits, not the
// process. That holds here for the whole life of the process: Light does not
// lock its goroutine to a thread, and the Go runtime ends a thread only when a
// goroutine locked to it exits, so the thread that forked the child lives as
// long as this process does. A caller that locks its own goroutine to a thread
// and lets that goroutine return without unlocking would end its thread and
// the children it started with it.
func dieWithParent(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
