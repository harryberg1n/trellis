package kernel

import "time"

// Table is the shipped feature table: the walls and policies. The deny lines
// and the matching are in internal/walls; a row here only says that it exists,
// when it acts and who decided it. Every Decision is "<YYYY-MM-DD> <owner>".
var Table = []Row{
	{
		ID: "primary-write", Kind: Wall, Stage: StageEdit, State: Admitted, Level: Block,
		Incident: "#1043", Decision: "2026-09-01 owner", Budget: 50 * time.Millisecond, Class: Light,
	},
	{
		ID: "main-commit", Kind: Wall, Stage: StageCommit, State: Admitted, Level: Block,
		Incident: "#1043", Decision: "2026-09-01 owner", Budget: 50 * time.Millisecond, Class: Light,
	},
	{
		ID: "hook-bypass", Kind: Wall, Stage: StageCommit, State: Admitted, Level: Block,
		Incident: "2026-09 bypass refusals (no-verify)", Decision: "2026-09-01 owner",
		Budget: 50 * time.Millisecond, Class: Light,
	},
	{
		ID: "discard-uncommitted", Kind: Wall, Stage: StageEdit, State: Admitted, Level: Block,
		Incident: "#650, #857", Decision: "2026-09-01 owner", Budget: 2 * time.Second, Class: Light,
	},
	{
		ID: "own-git-state", Kind: Wall, Stage: StageSession, State: Admitted, Level: Block,
		Incident: "W42 test-map worktree mutation (fanpyp report 2026-10-17)", Decision: "2026-10-17 owner",
		Budget: time.Millisecond, Class: Light,
	},
	{
		ID: "secrets", Kind: Wall, Stage: StageCommit, State: Admitted, Level: Block,
		Incident: "owner decision; gitleaks finding 2026-09", Decision: "2026-09-01 owner",
		Budget: 10 * time.Second, Class: Light,
	},
	{
		ID: "undercover", Kind: Policy, Stage: StageCommit, State: Admitted, Level: Block,
		Incident: "2026-09 owner", Decision: "2026-09-01 owner", Budget: 50 * time.Millisecond, Class: Light,
	},
	{
		ID: "walls-warn-people", Kind: Policy, Stage: StageSession, State: Admitted, Level: Block,
		Decision: "2026-10-01 owner", Class: Light,
	},
	{
		ID: "secrets-everyone", Kind: Policy, Stage: StageSession, State: Admitted, Level: Block,
		Decision: "2026-10-01 owner", Class: Light,
	},
}
