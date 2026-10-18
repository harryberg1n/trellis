//go:build windows

// Source: aphrollo-tools 12b3724e internal/tdd/shell/shellwrite.go (baseCommand).
// Incident: Windows resolves command names without case, so `Git reset --hard`
// and `GIT commit --no-verify` ran git past every wall that matched "git".

package walls

import "strings"

// foldName is the name the file system resolves a command by: Windows ignores
// case.
func foldName(name string) string { return strings.ToLower(name) }
