//go:build !windows

package objects

import "testing"

func skipNoExecBit(t *testing.T) { t.Helper() }
