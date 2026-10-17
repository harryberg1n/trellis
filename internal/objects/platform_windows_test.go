//go:build windows

package objects

import "testing"

func skipNoExecBit(t *testing.T) {
	t.Helper()
	t.Skip("Windows files carry no exec bit for Lstat to report")
}
