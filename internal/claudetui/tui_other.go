//go:build !darwin && !linux

package claudetui

import "errors"

// Run has no pseudo-terminal to run Claude Code in here: the bridge runs
// `claude -p` on this system.
func Run(args []string) error {
	return errors.New("Claude Code's interactive mode isn't run by magpie on this system")
}
