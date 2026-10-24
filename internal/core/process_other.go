//go:build !darwin && !linux

package core

import "os/exec"

// Other platforms retain exec.CommandContext's direct-process cancellation.
// Process-group cancellation is supported on the macOS target and on Linux.
func configureCancellation(cmd *exec.Cmd) {}
