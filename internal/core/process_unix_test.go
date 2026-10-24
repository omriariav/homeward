//go:build darwin || linux

package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// The test executable provides both local helper processes. No shell, Apple
// tooling, network client or AI CLI participates in this cancellation test.
func TestRunCLIProcessHelper(t *testing.T) {
	role := os.Getenv("HOMEWARD_CORE_PROCESS_HELPER")
	if role == "" {
		return
	}
	directory := os.Getenv("HOMEWARD_CORE_PROCESS_DIRECTORY")
	if role == "parent" {
		command := exec.Command(os.Args[0], "-test.run=^TestRunCLIProcessHelper$")
		command.Env = append(os.Environ(), "HOMEWARD_CORE_PROCESS_HELPER=child")
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err := command.Run(); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	if err := os.WriteFile(filepath.Join(directory, "child-ready"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(3)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(directory, "release-child")); err == nil {
			_ = os.WriteFile(filepath.Join(directory, "child-survived"), []byte("alive"), 0600)
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(4)
}

func TestRunCLICancellationTerminatesDescendants(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("HOMEWARD_CORE_PROCESS_HELPER", "parent")
	t.Setenv("HOMEWARD_CORE_PROCESS_DIRECTORY", directory)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := RunCLI(ctx, []string{os.Args[0], "-test.run=^TestRunCLIProcessHelper$"}, "private test input", directory)
		done <- err
	}()
	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(directory, "child-ready")); err == nil {
			childPID, err = strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("local child did not start")
	}
	// Ensure cleanup even if a regression leaves the child alive.
	defer syscall.Kill(childPID, syscall.SIGKILL)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation not propagated: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled process group did not finish")
	}
	if err := os.WriteFile(filepath.Join(directory, "release-child"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(directory, "child-survived")); err == nil {
			t.Fatal("CLI descendant survived cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
