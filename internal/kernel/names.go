package kernel

// The strings that cross a package boundary as data. The hook adapter writes
// them, the walls and the report read them; each is spelled here once.

// Tool names, as the harness writes tool_name.
const (
	ToolBash         = "Bash"
	ToolPowerShell   = "PowerShell"
	ToolEdit         = "Edit"
	ToolWrite        = "Write"
	ToolMultiEdit    = "MultiEdit"
	ToolNotebookEdit = "NotebookEdit"
)

// IsShellTool reports whether tool runs a command line: Bash or PowerShell.
func IsShellTool(tool string) bool { return tool == ToolBash || tool == ToolPowerShell }

// Keys of Context.Args, written by the hook adapter and read by the walls.
const (
	ArgCwd      = "cwd"       // the payload's working directory
	ArgRepo     = "repo"      // the root of the repository around it, when there is one
	ArgPerson   = "person"    // "1" when a person, not an agent, made the call
	ArgCommand  = "command"   // the command line of a shell tool
	ArgFilePath = "file_path" // the absolute, cleaned path of the file a file tool changes
	ArgPath     = "path"      // that file relative to the repo, forward slashes
)

// Keys of Event.Detail. The comments in event.go say which kind carries which.
const (
	DetailRow        = "row"
	DetailBytes      = "bytes"
	DetailLine       = "line"
	DetailResult     = "result"
	DetailPR         = "pr"
	DetailTranscript = "transcript"
	DetailReason     = "reason"
	DetailPath       = "path"
	DetailBash       = "bash"
	DetailBlob       = "blob"
	DetailTree       = "tree"
	DetailCommit     = "commit"
	DetailRef        = "ref"
	DetailRecording  = "recording"
	DetailStoreError = "store_error"
	DetailAfter      = "after"
)

// Keys of a harness payload's tool_input.
const (
	InputCommand      = "command"
	InputFilePath     = "file_path"
	InputNotebookPath = "notebook_path"
)

// Hook event names, as the harness writes hook_event_name.
const (
	HookPreToolUse         = "PreToolUse"
	HookPostToolUse        = "PostToolUse"
	HookStop               = "Stop"
	HookSessionStart       = "SessionStart"
	HookSessionEnd         = "SessionEnd"
	HookUserPromptSubmit   = "UserPromptSubmit"
	HookPostToolUseFailure = "PostToolUseFailure"
	HookSubagentStart      = "SubagentStart"
	HookSubagentStop       = "SubagentStop"
	HookNotification       = "Notification"
	HookPreCompact         = "PreCompact"
)

// HookEvents are the events the hook command accepts, in the harness's
// spelling. The last five carry nothing to record yet.
func HookEvents() []string {
	return []string{
		HookPreToolUse, HookPostToolUse, HookStop, HookSessionStart, HookSessionEnd, HookUserPromptSubmit,
		HookPostToolUseFailure, HookSubagentStart, HookSubagentStop, HookNotification, HookPreCompact,
	}
}

// InstalledHookEvents are the events install wires up, in install order.
func InstalledHookEvents() []string {
	return []string{HookPreToolUse, HookPostToolUse, HookStop, HookSessionStart, HookSessionEnd, HookUserPromptSubmit}
}
