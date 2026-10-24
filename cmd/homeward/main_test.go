package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHelpAndInvalidLiveTargetNeverNeedTools(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"tui", "--help"}, {"build", "--help"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, strings.NewReader(""), &out); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"tui", "--device", "synthetic"}, strings.NewReader("0\n"), &out); err == nil {
		t.Fatal("accepted unpaired live device")
	}
}
func TestStandalonePlanPreviewAndExactVerification(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "layout.json")
	draft := filepath.Join(dir, "draft.json")
	if err := os.WriteFile(layout, []byte(`[["phone"],["mail","books","games"]]`), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"plan", "--layout", layout, "--request", "mail books", "--output", draft, "--no-ai"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PROPOSED HOME SCREEN") {
		t.Fatal(out.String())
	}
	if err := run(context.Background(), []string{"preview", draft}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"verify-layout", "--expected", layout, "--actual", layout}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"plan", "--layout", layout, "--request", "mail", "--output", draft}, strings.NewReader(""), &out); err == nil {
		t.Fatal("overwrote saved proposal")
	}
}
func TestAIRefusalOnCLIDoesNotCreateDraft(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "layout.json")
	draft := filepath.Join(dir, "draft.json")
	_ = os.WriteFile(layout, []byte(`[["phone"],["mail","games"]]`), 0600)
	var out bytes.Buffer
	err := run(context.Background(), []string{"plan", "--layout", layout, "--request", "work", "--output", draft, "--ai"}, strings.NewReader("\n"), &out)
	if err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatal(err)
	}
	if _, err = os.Stat(draft); !os.IsNotExist(err) {
		t.Fatal("refusal wrote draft")
	}
	if !strings.Contains(out.String(), "Data sent") {
		t.Fatal("missing disclosure")
	}
}

func TestExplicitPlainModeKeepsScriptedInterface(t *testing.T) {
	var output bytes.Buffer
	err := run(context.Background(), []string{"tui", "--demo", "--plain", "--output-dir", t.TempDir()}, strings.NewReader("0\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "\x1b") || !strings.Contains(output.String(), "Homeward >") {
		t.Fatal(output.String())
	}
}

func TestCanonicalCancellationUnblocksInputAndJoinsWatcher(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := closeCanonicalInputOnCancel(ctx, reader)
	done := make(chan error, 1)
	go func() { var b [1]byte; _, err := reader.Read(b[:]); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canonical read stayed blocked")
	}
	stop()
}
func TestCanonicalWatcherDoesNotCloseInputAfterNormalReturn(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := closeCanonicalInputOnCancel(ctx, reader)
	stop()
	cancel()
	if _, err = writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err = reader.Read(b[:]); err != nil || b[0] != 'x' {
		t.Fatal("completed watcher closed input", err)
	}
}
