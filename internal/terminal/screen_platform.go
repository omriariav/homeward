//go:build darwin || (linux && (amd64 || arm64))

package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const screenPoll = 100 * time.Millisecond
const escapeDelay = 40 * time.Millisecond

type screenOps struct {
	get   func(uintptr) (syscall.Termios, error)
	set   func(uintptr, *syscall.Termios) error
	flush func(uintptr) error
	size  func(uintptr) (int, int)
	wait  func(int, time.Duration) (bool, error)
	read  func(int, []byte) (int, error)
}

func defaultScreenOps() screenOps {
	return screenOps{screenGetTermios, screenSetTermios, screenFlushInput, screenSize, screenWait, syscall.Read}
}

// ScreenTerminal owns a duplicate input descriptor so Close can restore the
// original termios even when cancellation has closed the caller's os.Stdin.
// It changes no cursor/screen styling; the renderer owns those ANSI sequences.
type ScreenTerminal struct {
	mu                sync.Mutex
	input, output     *os.File
	original          syscall.Termios
	ops               screenOps
	closed, suspended bool
	pending           []byte
	pendingAt         time.Time
}

func IsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	_, err := screenGetTermios(file.Fd())
	return err == nil
}

func OpenScreen(input, output *os.File) (*ScreenTerminal, error) {
	return openScreenWithOps(input, output, defaultScreenOps())
}
func openScreenWithOps(input, output *os.File, ops screenOps) (*ScreenTerminal, error) {
	if input == nil || output == nil {
		return nil, errors.New("interactive screen requires terminal input and output")
	}
	original, err := ops.get(input.Fd())
	if err != nil {
		return nil, fmt.Errorf("input is not a usable terminal: %w", err)
	}
	if _, err = ops.get(output.Fd()); err != nil {
		return nil, fmt.Errorf("output is not a usable terminal: %w", err)
	}
	descriptor, err := syscall.Dup(int(input.Fd()))
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(descriptor)
	if !screenFDValid(descriptor) {
		_ = syscall.Close(descriptor)
		return nil, errors.New("terminal descriptor exceeds select capacity; use --plain")
	}
	owned := os.NewFile(uintptr(descriptor), "homeward-terminal")
	screen := &ScreenTerminal{input: owned, output: output, original: original, ops: ops}
	raw := screenRaw(original)
	if err = ops.set(owned.Fd(), &raw); err != nil {
		// Even a partially successful transition must attempt restoration.
		restoreErr := ops.set(owned.Fd(), &original)
		_ = owned.Close()
		return nil, errors.Join(fmt.Errorf("enable raw terminal: %w", err), restoreErr)
	}
	return screen, nil
}

func screenRaw(original syscall.Termios) syscall.Termios {
	raw := original
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON | syscall.IXOFF
	raw.Oflag &^= syscall.OPOST
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	// select provides polling. Nonblocking terminal reads avoid races with a flush.
	raw.Cc[syscall.VMIN] = 0
	raw.Cc[syscall.VTIME] = 0
	return raw
}

func (s *ScreenTerminal) Size() (width, height int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 80, 24
	}
	return s.ops.size(s.output.Fd())
}

func (s *ScreenTerminal) FlushInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	s.pending = nil
	s.pendingAt = time.Time{}
	return s.ops.flush(s.input.Fd())
}

func (s *ScreenTerminal) ReadKey(ctx context.Context) (Key, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Key{}, err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return Key{}, os.ErrClosed
		}
		if s.suspended {
			s.mu.Unlock()
			return Key{}, errors.New("terminal key input is suspended")
		}
		final := len(s.pending) > 0 && time.Since(s.pendingAt) >= escapeDelay
		if key, n, ok := decodeKey(s.pending, final); ok {
			s.pending = s.pending[n:]
			if len(s.pending) == 0 {
				s.pendingAt = time.Time{}
			}
			s.mu.Unlock()
			return key, nil
		}
		fd := int(s.input.Fd())
		wait := screenPoll
		if len(s.pending) > 0 {
			wait = escapeDelay - time.Since(s.pendingAt)
			if wait < 0 {
				wait = 0
			}
		}
		s.mu.Unlock()
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return Key{}, context.DeadlineExceeded
			}
			if remaining < wait {
				wait = remaining
			}
		}
		ready, err := s.ops.wait(fd, wait)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return Key{}, err
		}
		if !ready {
			if err := ctx.Err(); err != nil {
				return Key{}, err
			}
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return Key{}, os.ErrClosed
			}
			empty := len(s.pending) == 0
			s.mu.Unlock()
			if empty {
				return Key{Name: "tick"}, nil
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return Key{}, err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return Key{}, os.ErrClosed
		}
		var buffer [1]byte
		n, err := s.ops.read(fd, buffer[:])
		if n > 0 {
			if len(s.pending) == 0 {
				s.pendingAt = time.Now()
			}
			s.pending = append(s.pending, buffer[0])
		}
		s.mu.Unlock()
		if err != nil {
			if errors.Is(err, syscall.EIO) {
				return Key{Name: "eof"}, nil
			}
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
				continue
			}
			return Key{}, err
		}
		if n == 0 {
			return Key{Name: "eof"}, nil
		}
	}
}

// Suspend restores canonical terminal state for a callback, then re-enters raw
// mode even if the callback errors or panics. Close inside the callback wins.
func (s *ScreenTerminal) Suspend(callback func() error) (err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return os.ErrClosed
	}
	if s.suspended {
		s.mu.Unlock()
		return errors.New("terminal is already suspended")
	}
	if err = s.ops.set(s.input.Fd(), &s.original); err != nil {
		s.mu.Unlock()
		return err
	}
	s.suspended = true
	s.pending = nil
	s.pendingAt = time.Time{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.suspended = false
		if !s.closed {
			raw := screenRaw(s.original)
			err = errors.Join(err, s.ops.set(s.input.Fd(), &raw))
		}
	}()
	return callback()
}

func (s *ScreenTerminal) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.pending = nil
	restoreErr := s.ops.set(s.input.Fd(), &s.original)
	return errors.Join(restoreErr, s.input.Close())
}

func screenFDValid(fd int) bool {
	var set syscall.FdSet
	bits := int(unsafe.Sizeof(set.Bits[0]) * 8)
	return fd >= 0 && fd/bits < len(set.Bits)
}
func screenWait(fd int, duration time.Duration) (bool, error) {
	var set syscall.FdSet
	bits := int(unsafe.Sizeof(set.Bits[0]) * 8)
	index := fd / bits
	if !screenFDValid(fd) {
		return false, errors.New("terminal descriptor exceeds select capacity")
	}
	set.Bits[index] |= 1 << uint(fd%bits)
	timeout := syscall.NsecToTimeval(duration.Nanoseconds())
	if err := screenSelect(fd+1, &set, &timeout); err != nil {
		return false, err
	}
	return set.Bits[index]&(1<<uint(fd%bits)) != 0, nil
}

func screenSize(fd uintptr) (int, int) {
	var size struct{ Row, Col, Xpixel, Ypixel uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&size)))
	if errno != 0 || size.Col == 0 || size.Row == 0 {
		return 80, 24
	}
	return int(size.Col), int(size.Row)
}
