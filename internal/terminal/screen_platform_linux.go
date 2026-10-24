//go:build linux && (amd64 || arm64)

package terminal

import (
	"syscall"
	"unsafe"
)

func screenGetTermios(fd uintptr) (syscall.Termios, error) {
	var state syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&state)))
	if errno != 0 {
		return state, errno
	}
	return state, nil
}
func screenSetTermios(fd uintptr, state *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(state)))
	if errno != 0 {
		return errno
	}
	return nil
}
func screenFlushInput(fd uintptr) error {
	// TCFLSH on Linux amd64/arm64; frozen syscall omits it on amd64.
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, 0x540b, syscall.TCIFLUSH)
	if errno != 0 {
		return errno
	}
	return nil
}
func screenSelect(n int, set *syscall.FdSet, timeout *syscall.Timeval) error {
	_, err := syscall.Select(n, set, nil, nil, timeout)
	return err
}
