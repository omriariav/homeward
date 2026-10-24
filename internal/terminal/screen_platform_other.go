//go:build !darwin && (!linux || (!amd64 && !arm64))

package terminal

import (
	"context"
	"errors"
	"os"
)

type ScreenTerminal struct{}

func IsTerminal(*os.File) bool { return false }
func OpenScreen(*os.File, *os.File) (*ScreenTerminal, error) {
	return nil, errors.New("keyboard screen is supported on macOS and Linux amd64/arm64; use --plain")
}
func (*ScreenTerminal) Size() (int, int) { return 80, 24 }
func (*ScreenTerminal) ReadKey(context.Context) (Key, error) {
	return Key{}, errors.New("keyboard screen unavailable")
}
func (*ScreenTerminal) FlushInput() error                   { return nil }
func (*ScreenTerminal) Suspend(callback func() error) error { return callback() }
func (*ScreenTerminal) Close() error                        { return nil }
