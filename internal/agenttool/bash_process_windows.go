//go:build windows

package agenttool

import "os/exec"

// Windows keeps exec.CommandContext's native process termination behavior.
func configureCommandCancellation(_ *exec.Cmd) {}
