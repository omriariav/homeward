//go:build darwin || linux

package device

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// RunCommand starts a process group so Ctrl-C/timeouts terminate host-side tool
// descendants as well. A device-side XCTest may still finish after disconnection;
// callers must report partial/unknown state after any attempted mutation.
func RunCommand(ctx context.Context, args []string) ([]byte, []byte, error) {
	if len(args) == 0 {
		return nil, nil, errors.New("empty subprocess argv")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = os.Environ()
	hasDeveloperDir := false
	for _, value := range cmd.Env {
		if strings.HasPrefix(value, "DEVELOPER_DIR=") {
			hasDeveloperDir = true
			break
		}
	}
	if !hasDeveloperDir {
		cmd.Env = append(cmd.Env, "DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		// Kill the entire host process group immediately; do not leave xcodebuild
		// children holding pipes or sending more device gestures after cancellation.
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
