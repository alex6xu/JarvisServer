//go:build !windows

package agenttool

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureCommandCancellation puts the shell and every child it launches into
// a separate process group. exec.CommandContext otherwise kills only the shell,
// allowing compilers/test workers to survive a timeout and exhaust RAM/disk.
func configureCommandCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
