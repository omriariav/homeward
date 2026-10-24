//go:build darwin

package terminal

import (
	"syscall"
	"unsafe"
)

func screenGetTermios(fd uintptr) (syscall.Termios, error) {
	var state syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGETA, uintptr(unsafe.Pointer(&state)))
	if errno != 0 {
		return state, errno
	}
	return state, nil
}
func screenSetTermios(fd uintptr, state *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSETA, uintptr(unsafe.Pointer(state)))
	if errno != 0 {
		return errno
	}
	return nil
}
func screenFlushInput(fd uintptr) error {
	direction := int32(1) // FREAD: discard pending input, preserving output.
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCFLUSH, uintptr(unsafe.Pointer(&direction)))
	if errno != 0 {
		return errno
	}
	return nil
}
func screenSelect(n int, set *syscall.FdSet, timeout *syscall.Timeval) error {
	return syscall.Select(n, set, nil, nil, timeout)
}
