//go:build !windows

package run

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
)

func TestLight_CancelAsksTheGroupToTerminateAndKillsItAfterTheWaitDelay(t *testing.T) {
	accept := reportedPid(t, "stubborn") // ignores SIGTERM
	fc := clock.Fake(time.Unix(1000, 0))
	t.Cleanup(func() { fc.Advance(time.Hour) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Light(fc, Spec{Name: os.Args[0], Timeout: time.Hour, Ctx: ctx})
		done <- err
	}()
	pid := accept()
	fc.BlockUntil(1) // the timeout timer

	cancel()
	fc.BlockUntil(2) // the wait-delay timer: SIGTERM has been sent by now

	select {
	case err := <-done:
		t.Fatalf("Light returned %v before the wait delay: the child ignores SIGTERM and only SIGKILL ends it", err)
	default:
	}
	fc.Advance(waitDelay)
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	for i := 0; alive(pid); i++ {
		if i == 5_000_000 {
			t.Fatalf("the stubborn child %d survived the kill", pid)
		}
	}
}
