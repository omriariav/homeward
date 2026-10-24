//go:build darwin

package terminal

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// A real native PTY checks termios and key input without shells, stty, a phone,
// external services, or third-party packages. Restricted hosts may deny PTYs.
func nativePTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fd, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("PTY unavailable: %v", err)
	}
	master := os.NewFile(uintptr(fd), "homeward-test-pty-master")
	t.Cleanup(func() { _ = master.Close() })
	for _, request := range []uintptr{syscall.TIOCPTYGRANT, syscall.TIOCPTYUNLK} {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), request, 0)
		if errno != 0 {
			t.Skipf("PTY setup unavailable: %v", errno)
		}
	}
	var name [128]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0])))
	if errno != 0 {
		t.Skipf("PTY name unavailable: %v", errno)
	}
	end := bytes.IndexByte(name[:], 0)
	if end < 0 {
		t.Fatal("invalid PTY name")
	}
	slave, err := os.OpenFile(string(name[:end]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}
func TestNativePTYKeysResizeFlushAndRestoration(t *testing.T) {
	master, slave := nativePTY(t)
	before, err := screenGetTermios(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if !IsTerminal(master) || !IsTerminal(slave) {
		t.Fatal("native PTY not recognized")
	}
	screen, err := OpenScreen(slave, slave)
	if err != nil {
		t.Fatal(err)
	}
	defer screen.Close()
	for _, tc := range []struct {
		bytes, name string
		character   rune
	}{{"\x1b[A", "up", 0}, {"ש", "rune", 'ש'}, {"\x1b", "esc", 0}, {"\x03", "ctrl-c", 0}, {"\x04", "eof", 0}} {
		if _, err = master.Write([]byte(tc.bytes)); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		key, err := screen.ReadKey(ctx)
		cancel()
		if err != nil || key.Name != tc.name || key.Rune != tc.character {
			t.Fatalf("%q: %#v %v", tc.bytes, key, err)
		}
	}
	if _, err = master.Write([]byte("APPLY buffered\n")); err != nil {
		t.Fatal(err)
	}
	// Wait until the kernel queues bytes before discarding them.
	if ready, err := screenWait(int(slave.Fd()), time.Second); err != nil || !ready {
		t.Fatal("input not queued", err)
	}
	if err = screen.FlushInput(); err != nil {
		t.Fatal(err)
	}
	if key, err := screen.ReadKey(context.Background()); err != nil || key.Name != "tick" {
		t.Fatal("flush did not discard input", key, err)
	}
	var size struct{ Rows, Columns, Xpixel, Ypixel uint16 }
	size.Rows = 37
	size.Columns = 111
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&size)))
	if errno != 0 {
		t.Fatal(errno)
	}
	if width, height := screen.Size(); width != 111 || height != 37 {
		t.Fatal(width, height)
	}
	if err = screen.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := screenGetTermios(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("PTY termios changed after close")
	}
	if _, err = screen.ReadKey(context.Background()); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
}

func TestScreenPTYProcessHelper(t *testing.T) {
	if os.Getenv("HOMEWARD_PTY_HELPER") != "1" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	app, err := New(Options{Root: os.Getenv("HOMEWARD_PTY_DIR"), OutputDir: os.Getenv("HOMEWARD_PTY_DIR"), Demo: true}, NewTerminal(os.Stdin, os.Stdout, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	screen, err := OpenScreen(os.Stdin, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	defer screen.Close()
	// Raw input polls ctx and does not need concurrent closure of os.Stdin.
	if err = RunScreen(ctx, app, screen, os.Stdout, false); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativePTYFullScreenRestoresAfterQuitAndInterrupt(t *testing.T) {
	for _, mode := range []string{"q", "literal-ctrl-c", "signal-ctrl-c"} {
		t.Run(mode, func(t *testing.T) {
			master, slave := nativePTY(t)
			// Model a normal canonical terminal without a preexisting retype request.
			original, err := screenGetTermios(slave.Fd())
			if err != nil {
				t.Fatal(err)
			}
			original.Lflag &^= syscall.PENDIN
			if err = screenSetTermios(slave.Fd(), &original); err != nil {
				t.Fatal(err)
			}
			before, err := screenGetTermios(slave.Fd())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestScreenPTYProcessHelper$", "-test.count=1")
			command.Env = append(os.Environ(), "HOMEWARD_PTY_HELPER=1", "HOMEWARD_PTY_DIR="+t.TempDir())
			command.Stdin, command.Stdout, command.Stderr = slave, slave, slave
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			var output []byte
			for !bytes.Contains(output, []byte("HOMEWARD")) {
				if err = ctx.Err(); err != nil {
					_ = command.Process.Kill()
					_ = command.Wait()
					t.Fatal("dashboard did not appear", err)
				}
				ready, err := screenWait(int(master.Fd()), 100*time.Millisecond)
				if errors.Is(err, syscall.EINTR) {
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if !ready {
					continue
				}
				var data [8192]byte
				n, err := syscall.Read(int(master.Fd()), data[:])
				if err != nil {
					t.Fatal(err)
				}
				output = append(output, data[:n]...)
			}
			switch mode {
			case "q":
				_, err = master.Write([]byte("q"))
			case "literal-ctrl-c":
				_, err = master.Write([]byte{3})
			case "signal-ctrl-c":
				err = command.Process.Signal(os.Interrupt)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Keep draining after the first frame. A macOS PTY session leader
			// can wait for queued output during exit, even after interruption.
			waited := make(chan error, 1)
			go func() { waited <- command.Wait() }()
			complete := false
			for !complete {
				select {
				case err = <-waited:
					complete = true
				default:
					if ctx.Err() != nil {
						_ = master.Close()
						select {
						case err = <-waited:
							complete = true
						case <-time.After(time.Second):
							t.Fatal("PTY child did not terminate")
						}
						break
					}
					ready, readErr := screenWait(int(master.Fd()), 20*time.Millisecond)
					if errors.Is(readErr, syscall.EINTR) {
						continue
					}
					if readErr != nil {
						t.Fatal(readErr)
					}
					if ready {
						var data [8192]byte
						n, readErr := syscall.Read(int(master.Fd()), data[:])
						if readErr != nil && !errors.Is(readErr, syscall.EIO) {
							t.Fatal(readErr)
						}
						output = append(output, data[:n]...)
					}
				}
			}
			if err != nil {
				t.Fatalf("screen child failed: %v; output %q", err, output)
			}
			after, err := screenGetTermios(slave.Fd())
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("termios lflag before=%#x after=%#x diff=%#x", before.Lflag, after.Lflag, before.Lflag^after.Lflag)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("termios changed: before=%#v after=%#v", before, after)
			}
		})
	}
}
