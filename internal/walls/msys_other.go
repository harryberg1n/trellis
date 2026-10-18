//go:build !windows

package walls

// msysDrive is false off Windows: /d/x is the path it spells.
func msysDrive(string) (string, bool) { return "", false }
