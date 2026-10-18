//go:build !windows

package walls

import "testing"

func TestResolve_LeavesAnMSYSLookingPathAloneOffWindows(t *testing.T) {
	if got := resolve("/w", "/d/Projects/x"); got != "/d/Projects/x" {
		t.Errorf("resolve = %q, want /d/Projects/x", got)
	}
}
