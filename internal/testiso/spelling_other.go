//go:build !windows

package testiso

// shortSpelling is "": only Windows has short names.
func shortSpelling(string) string { return "" }
