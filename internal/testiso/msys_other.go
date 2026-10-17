// Copied from aphrollo-tools internal/msys/anchor_other.go at 12b3724e.
// Incident #1043: tests wrote into the real repository and the global git config.

//go:build !windows

package testiso

// startAnchor has nothing to start: only Windows has the shared /tmp mount.
func startAnchor() (release func(), exited <-chan struct{}) {
	done := make(chan struct{})
	close(done)
	return func() {}, done
}
