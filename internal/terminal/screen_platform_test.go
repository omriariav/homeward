//go:build darwin || (linux && (amd64 || arm64))

package terminal

import (
	"context"
	"errors"
	"os"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func screenFixture(t *testing.T) (*os.File, screenOps, syscall.Termios, *[]syscall.Termios) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "fake-terminal")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	original := syscall.Termios{}
	original.Iflag = syscall.ICRNL | syscall.IXON | syscall.IXOFF
	original.Oflag = syscall.OPOST
	original.Cflag = syscall.CS8
	original.Lflag = syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	original.Cc[syscall.VMIN] = 1
	states := []syscall.Termios{}
	ops := screenOps{get: func(uintptr) (syscall.Termios, error) { return original, nil }, set: func(fd uintptr, state *syscall.Termios) error {
		var info syscall.Stat_t
		if err := syscall.Fstat(int(fd), &info); err != nil {
			return err
		}
		states = append(states, *state)
		return nil
	}, flush: func(uintptr) error { return nil }, size: func(uintptr) (int, int) { return 92, 31 }, wait: func(int, time.Duration) (bool, error) { return false, nil }, read: func(int, []byte) (int, error) { return 0, nil }}
	return file, ops, original, &states
}
func TestScreenRestoresOwnedDescriptorAfterCallerClosesInput(t *testing.T) {
	file, ops, original, states := screenFixture(t)
	screen, err := openScreenWithOps(file, file, ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(*states) != 1 || (*states)[0].Lflag&(syscall.ECHO|syscall.ICANON|syscall.ISIG) != 0 {
		t.Fatal("raw mode missing", states)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = screen.Close(); err != nil {
		t.Fatal(err)
	}
	if err = screen.Close(); err != nil {
		t.Fatal(err)
	}
	if len(*states) != 2 || !reflect.DeepEqual((*states)[1], original) {
		t.Fatal("original termios not restored exactly", states)
	}
}
func TestScreenFailureAttemptsRestorationAndClosesOwnedFD(t *testing.T) {
	file, ops, original, _ := screenFixture(t)
	calls := 0
	fd := uintptr(0)
	ops.set = func(descriptor uintptr, state *syscall.Termios) error {
		fd = descriptor
		calls++
		if calls == 1 {
			return errors.New("raw setup failed")
		}
		if !reflect.DeepEqual(*state, original) {
			t.Fatal("did not restore original state")
		}
		return nil
	}
	if _, err := openScreenWithOps(file, file, ops); err == nil {
		t.Fatal("accepted raw failure")
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	var info syscall.Stat_t
	if err := syscall.Fstat(int(fd), &info); !errors.Is(err, syscall.EBADF) {
		t.Fatal("owned descriptor leaked", err)
	}
}
func TestScreenSuspendRestoresAcrossErrorAndPanic(t *testing.T) {
	for _, scenario := range []string{"error", "panic", "close"} {
		t.Run(scenario, func(t *testing.T) {
			file, ops, original, states := screenFixture(t)
			screen, err := openScreenWithOps(file, file, ops)
			if err != nil {
				t.Fatal(err)
			}
			defer screen.Close()
			invoke := func() {
				_ = screen.Suspend(func() error {
					if !reflect.DeepEqual((*states)[len(*states)-1], original) {
						t.Fatal("callback not canonical")
					}
					switch scenario {
					case "panic":
						panic("test panic")
					case "close":
						return screen.Close()
					default:
						return errors.New("test callback error")
					}
				})
			}
			if scenario == "panic" {
				func() {
					defer func() {
						if recover() == nil {
							t.Fatal("panic swallowed")
						}
					}()
					invoke()
				}()
			} else {
				invoke()
			}
			if scenario != "close" {
				last := (*states)[len(*states)-1]
				if last.Lflag&syscall.ICANON != 0 {
					t.Fatal("did not resume raw mode")
				}
			}
			if err = screen.Close(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual((*states)[len(*states)-1], original) {
				t.Fatal("final state not restored")
			}
		})
	}
}
func TestScreenFlushAndPollingCancellation(t *testing.T) {
	file, ops, _, _ := screenFixture(t)
	flushes := 0
	ops.flush = func(uintptr) error { flushes++; return nil }
	screen, err := openScreenWithOps(file, file, ops)
	if err != nil {
		t.Fatal(err)
	}
	defer screen.Close()
	screen.pending = []byte("yes")
	screen.pendingAt = time.Now()
	if err = screen.FlushInput(); err != nil {
		t.Fatal(err)
	}
	if len(screen.pending) != 0 || flushes != 1 {
		t.Fatal("buffered consent input was retained")
	}
	key, err := screen.ReadKey(context.Background())
	if err != nil || key.Name != "tick" {
		t.Fatal(key, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = screen.ReadKey(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if width, height := screen.Size(); width != 92 || height != 31 {
		t.Fatal(width, height)
	}
}
func TestScreenRejectsNonTTYWithoutChangingIt(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if IsTerminal(file) || IsTerminal(nil) {
		t.Fatal("regular file treated as terminal")
	}
	if screen, err := OpenScreen(file, file); err == nil {
		_ = screen.Close()
		t.Fatal("opened a regular file as terminal")
	}
}

func TestScreenSelectRejectsOutOfRangeDescriptors(t *testing.T) {
	for _, fd := range []int{-1, 1 << 30} {
		if _, err := screenWait(fd, time.Millisecond); err == nil {
			t.Fatalf("accepted select descriptor %d", fd)
		}
	}
}
func TestScreenOutputProbeFailureDoesNotChangeInput(t *testing.T) {
	file, ops, _, states := screenFixture(t)
	calls := 0
	originalGet := ops.get
	ops.get = func(fd uintptr) (syscall.Termios, error) {
		calls++
		if calls == 2 {
			return syscall.Termios{}, errors.New("output is not terminal")
		}
		return originalGet(fd)
	}
	if _, err := openScreenWithOps(file, file, ops); err == nil {
		t.Fatal("accepted broken output terminal")
	}
	if len(*states) != 0 {
		t.Fatal("input mode changed despite output failure")
	}
}
