package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
