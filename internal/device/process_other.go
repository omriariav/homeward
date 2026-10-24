//go:build !darwin && !linux

package device

import (
	"context"
	"errors"
)

func RunCommand(context.Context, []string) ([]byte, []byte, error) {
	return nil, nil, errors.New("live Apple device commands require macOS; local simulation remains available")
}
