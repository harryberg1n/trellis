package walls

import "strings"

// allowPeople is the allow-people wall (owner decision 2026-10-18): `trellis
// allow` waives a deny, and an agent must not waive its own. A person's
// terminal is not hooked, so the only `trellis allow` the hook ever sees is
// the agent's. The word `trellis` (or a path ending in it, the pinned
// binary's included) followed by `allow` as the command of a segment (behind env, sudo and the
// like, and inside the shell constructs the walk follows) is refused, and the row it names, when it names one, is in the cause.
func allowPeople(a *analysis) string {
	if !a.isShell() || !strings.Contains(a.command, "allow") {
		return ""
	}
	var cause string
	walk(a.tool, a.command, a.cwd, false, func(seg []word, _ string) {
		if cause != "" {
			return
		}
		i := commandStart(seg)
		if i+1 >= len(seg) || baseCommand(seg[i].text) != "trellis" || seg[i+1].text != "allow" {
			return
		}
		row := "<row>"
		if i+2 < len(seg) && seg[i+2].text != "" {
			row = seg[i+2].text
		}
		cause = "allow is for a person: ask them to run trellis allow " + row
	})
	return cause
}
