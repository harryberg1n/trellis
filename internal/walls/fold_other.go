// Source: aphrollo-tools 12b3724e internal/tdd/shell/shellwrite.go (baseCommand).
// Incident: Windows resolves command names without case; elsewhere `Git` is
// not `git`, so a name stays as it was written.

//go:build !windows

package walls

// foldName is name unchanged: command names are case-sensitive here.
func foldName(name string) string { return name }
