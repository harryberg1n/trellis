// Copied from aphrollo-tools internal/msys/msys_test.go at 12b3724e.
// Incident #1043: tests wrote into the real repository and the global git config.

package testiso

import "testing"

// Anchor is safe to call from every test binary and every run: once, silently,
// and on a box with no shell it does nothing.
func TestAnchor_CanBeCalledRepeatedly(_ *testing.T) {
	anchor()
	anchor()
}

// Letting the anchor go twice, or before it was ever started, is harmless.
func TestRelease_IsSafeAnyNumberOfTimes(_ *testing.T) {
	release()
	anchor()
	release()
	release()
}
