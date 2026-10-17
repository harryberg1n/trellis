package kernel

import "testing"

func TestEvInternalError_IsTheLoggedKind(t *testing.T) {
	if EvInternalError != "internal.error" {
		t.Errorf("EvInternalError = %q, want %q", EvInternalError, "internal.error")
	}
}
