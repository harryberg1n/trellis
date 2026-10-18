//go:build windows

// Copied from aphrollo-tools internal/run/tree_windows.go at 12b3724e.
// Incident: #997, a timed-out child's descendants kept running (taskkill /T
// misses an MSYS grandchild). The child starts suspended, joins a job object
// with KILL_ON_JOB_CLOSE, and runs only once it is inside; the job holds every
// process the child starts. The memory cap of the original is left out: M1.
package run

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobTree guards a child with a job object that kills on close. A light child
// that exits on its own leaves what it started alone: finish clears the flag
// before closing, as a light child's own exit has always done.
type jobTree struct {
	mu  sync.Mutex
	job windows.Handle
}

func prepare(cmd *exec.Cmd) (tree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if err := setJobLimits(job, &info); err != nil {
		_ = windows.CloseHandle(job) // the job was never used
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	return &jobTree{job: job}, nil
}

func setJobLimits(job windows.Handle, info *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error {
	_, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(info)), uint32(unsafe.Sizeof(*info)))
	return err
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

func (j *jobTree) attach(p *os.Process) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(p.Pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }() // a handle opened here for the assignment
	if err := windows.AssignProcessToJobObject(j.job, h); err != nil {
		return err
	}
	if status, _, _ := ntResumeProcess.Call(uintptr(h)); status != 0 {
		return errors.New("run: could not resume the child after it joined its job")
	}
	return nil
}

func (j *jobTree) kill() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.job != 0 {
		_ = windows.TerminateJobObject(j.job, 1) // a refusal leaves finish to close it
	}
}

func (j *jobTree) finish() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.job == 0 {
		return
	}
	// With the kill-on-close flag cleared, closing the handle ends nothing. A
	// clear that is refused leaves the flag, and the close then ends the tree.
	_ = setJobLimits(j.job, new(windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION))
	_ = windows.CloseHandle(j.job)
	j.job = 0
}

// terminate ends the job at once: a job object has no gentler signal.
func (j *jobTree) terminate() { j.kill() }
