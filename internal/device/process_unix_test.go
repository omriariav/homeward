//go:build darwin || linux

package device

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProcessHelper(t *testing.T) {
	if os.Getenv("HOMEWARD_PROCESS_HELPER") != "1" {
		return
	}
	os.Stdout.WriteString("helper started\n")
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestRealProcessCancellationIsBounded(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOMEWARD_PROCESS_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	stdout, _, err := RunCommand(ctx, []string{executable, "-test.run=^TestProcessHelper$"})
	if err == nil {
		t.Fatal("timeout unexpectedly succeeded")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("cancellation was not bounded")
	}
	if !strings.Contains(string(stdout), "helper started") {
		t.Fatalf("did not capture partial stdout: %s", stdout)
	}
}
