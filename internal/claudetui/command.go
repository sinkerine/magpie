package claudetui

// Command is the hidden subcommand that runs it.
const Command = "claude-tui-helper"

// TrustEnv, "1", has the shim tell Claude Code yes when it asks whether
// to trust the folder it runs in: the bridge sets it for magpie's own
// folder, and for a caller's in an account folder of magpie's, whose
// Claude Code no one else runs. Otherwise the question is the user's to
// answer in their own Claude Code, and the run is -p.
const TrustEnv = "MAGPIE_CLAUDE_TUI_TRUST"
