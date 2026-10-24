//go:build darwin || linux

package core

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureCancellation isolates the provider and its local descendants in one
// process group. Cancellation stops the whole group rather than leaving a child
// CLI holding pipes or continuing a request after the parent has returned.
func configureCancellation(cmd *exec.Cmd) {
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
