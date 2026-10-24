package device

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func fixtureLive(t *testing.T) *Live {
	t.Helper()
	return NewLive(t.TempDir(), "test-device", "0xABC", t.TempDir(), "/path with spaces/cfgutil")
}

func writeDeviceResult(t *testing.T, args []string, result string) {
	t.Helper()
	for i, arg := range args {
		if arg == "--json-output" {
			if err := os.WriteFile(args[i+1], []byte(result), 0600); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("missing JSON destination")
}

func contains(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

func fixtureRunner(t *testing.T, live *Live) string {
	t.Helper()
	root := filepath.Join(live.Root, ".build", "device", "Build", "Products")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "HomeScreenPOC_iphoneos.xctestrun")
	if err := os.WriteFile(path, []byte("synthetic plist"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

const runnerJSON = `{"TestConfigurations":[{"TestTargets":[{"TestBundlePath":"__TESTROOT__/app.xctest","EnvironmentVariables":{"EXISTING":"keep"}}]}]}`

func TestCaptureUsesCompleteCatalogAndProtectsShortcuts(t *testing.T) {
	live := fixtureLive(t)
	var commands [][]string
	live.Run = func(ctx context.Context, args []string) ([]byte, []byte, error) {
		commands = append(commands, args)
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("subprocess is unbounded")
		}
		switch {
		case contains(args, "UDID"):
			return []byte(`{"Output":{"Errors":{"0xABC":{}},"0xABC":{"UDID":"test-device"}}}`), nil, nil
		case contains(args, "get-icon-layout"):
			return []byte(`[["phone"],[["Work",["mail","hidden"]],"/"]]`), nil, nil
		case contains(args, "apps"):
			writeDeviceResult(t, args, `{"info":{"outcome":"success"},"result":{"apps":[
                {"bundleIdentifier":"phone","name":"Phone"},
                {"bundleIdentifier":"mail","name":"Mail"},
                {"bundleIdentifier":"hidden","name":"Hidden","hidden":true},
                {"bundleIdentifier":"library","name":"Library"},
                {"bundleIdentifier":"internal","internalApp":true},
                {"bundleIdentifier":"clip","appClip":true},
                {"bundleIdentifier":"hidden-library","hidden":true}]}}`)
			return []byte("Wrote JSON."), nil, nil
		default:
			t.Fatalf("unexpected command (cfgutil installedApps must not be needed): %v", args)
			return nil, nil, nil
		}
	}
	snapshot, err := live.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Identity["ecid"] != "0xabc" || snapshot.Identity["udid"] != "test-device" {
		t.Fatal(snapshot.Identity)
	}
	if got, want := snapshot.StageIDs, []string{"hidden", "library", "mail", "phone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stage: got %v want %v", got, want)
	}
	if len(snapshot.AppIDs) != 7 || len(snapshot.Warnings) != 1 {
		t.Fatalf("unexpected catalog: %+v", snapshot)
	}
	if snapshot.Labels["mail"] != "Mail" {
		t.Fatal(snapshot.Labels)
	}
	if len(commands) != 3 {
		t.Fatal(commands)
	}
	for _, args := range commands {
		if contains(args, "set-icon-layout") || contains(args, "xcodebuild") {
			t.Fatal("capture mutated the phone")
		}
	}
	logs, err := filepath.Glob(filepath.Join(live.EvidenceDir, "*.json"))
	if err != nil || len(logs) < 4 {
		t.Fatalf("missing private evidence: %v %v", logs, err)
	}
	for _, path := range logs {
		stat, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if stat.Mode().Perm() != 0600 {
			t.Fatalf("evidence permissions: %v", stat.Mode())
		}
	}
}

func TestCaptureRejectsDeviceMismatchBeforeReadingLayout(t *testing.T) {
	live := fixtureLive(t)
	calls := 0
	live.Run = func(context.Context, []string) ([]byte, []byte, error) {
		calls++
		return []byte(`{"Output":{"0xabc":{"UDID":"different-phone"}}}`), nil, nil
	}
	if _, err := live.Capture(context.Background()); err == nil || !strings.Contains(err.Error(), "same phone") {
		t.Fatalf("expected identity error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("identity mismatch made %d calls", calls)
	}
}

func TestCaptureRejectsIncompleteCatalog(t *testing.T) {
	for _, result := range []string{
		`{"info":{"outcome":"failure"},"result":{"apps":[{"bundleIdentifier":"phone"}]}}`,
		`{"info":{"outcome":"success"},"result":{"apps":[]}}`,
		`{"info":{"outcome":"success"},"result":{"apps":[{"name":"unknown"}]}}`,
	} {
		t.Run(result, func(t *testing.T) {
			live := fixtureLive(t)
			live.Run = func(_ context.Context, args []string) ([]byte, []byte, error) {
				if contains(args, "UDID") {
					return []byte(`{"Output":{"0xabc":{"UDID":"test-device"}}}`), nil, nil
				}
				if contains(args, "get-icon-layout") {
					return []byte(`[["phone"],["mail"]]`), nil, nil
				}
				writeDeviceResult(t, args, result)
				return nil, nil, nil
			}
			if _, err := live.Capture(context.Background()); err == nil {
				t.Fatal("accepted incomplete catalog")
			}
		})
	}
}

func TestPropertiesDoNotTrustErrorsOrFailedProcess(t *testing.T) {
	for _, response := range []string{
		`{"Output":{"Errors":{"0xabc":{"message":"failed"}},"0xabc":{"UDID":"test-device"}}}`,
		`{"Output":{"0xabc":{"UDID":"test-device"}},"Errors":{"0xabc":{"message":"failed"}}}`,
		`{"Output":{"0xabc":{"UDID":"test-device"},"0xABC":{"UDID":"test-device"}}}`,
	} {
		live := fixtureLive(t)
		live.Run = func(context.Context, []string) ([]byte, []byte, error) { return []byte(response), nil, nil }
		if _, err := live.property(context.Background(), "UDID"); err == nil {
			t.Fatal("accepted erroneous/ambiguous identity")
		}
	}
	live := fixtureLive(t)
	live.Run = func(context.Context, []string) ([]byte, []byte, error) {
		return []byte(`{"Output":{"0xabc":{"UDID":"test-device"}}}`), nil, errors.New("exit 1")
	}
	if _, err := live.property(context.Background(), "UDID"); err == nil {
		t.Fatal("accepted partial identity from failed process")
	}
}

func TestConfigureRunnerPreservesPathsAndRejectsAmbiguity(t *testing.T) {
	var data map[string]any
	if err := json.Unmarshal([]byte(runnerJSON), &data); err != nil {
		t.Fatal(err)
	}
	if err := configureRunner(data, map[string]any{"action": "hide-other-pages", "keepPages": 2}); err != nil {
		t.Fatal(err)
	}
	raw := string(mustJSON(t, data))
	for _, expected := range []string{"__TESTROOT__/app.xctest", `"EXISTING":"keep"`, "HSC_COMMAND", "hide-other-pages"} {
		if !strings.Contains(raw, expected) {
			t.Fatalf("lost %s: %s", expected, raw)
		}
	}
	for _, invalid := range []map[string]any{
		{},
		{"a": map[string]any{"TestBundlePath": "a"}, "b": map[string]any{"TestBundlePath": "b"}},
		{"a": map[string]any{"TestBundlePath": "a", "EnvironmentVariables": "wrong"}},
	} {
		if err := configureRunner(invalid, map[string]any{"action": "inspect"}); err == nil {
			t.Fatal("accepted ambiguous/invalid runner")
		}
	}
}

func TestPreflightRequiresRunnerAndExplicitUnlockedState(t *testing.T) {
	live := fixtureLive(t)
	live.Run = func(context.Context, []string) ([]byte, []byte, error) {
		t.Fatal("missing runner should be caught without device calls")
		return nil, nil, nil
	}
	if live.CleanupAvailable() {
		t.Fatal("runner unexpectedly available")
	}
	if err := live.Preflight(context.Background()); err == nil {
		t.Fatal("accepted missing runner")
	}
	fixtureRunner(t, live)
	for _, response := range []string{
		`{}`, `{"info":{"outcome":"success"},"result":{}}`,
		`{"info":{"outcome":"success"},"result":{"passcodeRequired":true}}`,
		`{"info":{"outcome":"success"},"result":{"passcodeRequired":false}}`,
	} {
		live.Run = func(_ context.Context, args []string) ([]byte, []byte, error) {
			if args[0] == "/usr/bin/plutil" {
				return []byte(runnerJSON), nil, nil
			}
			if !contains(args, "lockState") {
				t.Fatalf("unexpected operation: %v", args)
			}
			writeDeviceResult(t, args, response)
			return nil, nil, nil
		}
		err := live.Preflight(context.Background())
		accepted := strings.Contains(response, `"passcodeRequired":false`)
		if (err == nil) != accepted {
			t.Fatalf("preflight response %s: %v", response, err)
		}
	}
}

func TestPlaceBindsFileAndUsesLiteralArgv(t *testing.T) {
	live := fixtureLive(t)
	path := filepath.Join(t.TempDir(), "stage; $(touch nope).json")
	if err := os.WriteFile(path, []byte(`[["phone"],["mail"]]`), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	live.Run = func(_ context.Context, args []string) ([]byte, []byte, error) {
		calls++
		want := []string{live.Cfgutil, "--ecid", "0xabc", "--timeout", "15", "set-icon-layout", "--force", path}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("argv: got %v want %v", args, want)
		}
		return nil, nil, nil
	}
	layout := []any{[]any{"phone"}, []any{"mail"}}
	if err := live.Place(context.Background(), layout, path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`[["phone"],["changed"]]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := live.Place(context.Background(), layout, path); err == nil {
		t.Fatal("accepted changed staging file")
	}
	if calls != 1 {
		t.Fatalf("mutation called %d times", calls)
	}
}

func TestCleanupInvokesXCTestDirectlyAndRemovesTemporaryPlist(t *testing.T) {
	live := fixtureLive(t)
	source := fixtureRunner(t, live)
	var generated string
	calls := 0
	live.Run = func(_ context.Context, args []string) ([]byte, []byte, error) {
		calls++
		if strings.Contains(strings.Join(args, " "), "python") {
			t.Fatal("Go device runtime invoked Python")
		}
		if args[0] == "/usr/bin/plutil" && contains(args, "json") {
			return []byte(runnerJSON), nil, nil
		}
		if args[0] == "/usr/bin/plutil" && contains(args, "xml1") {
			generated = args[len(args)-1]
			if filepath.Dir(generated) != filepath.Dir(source) {
				t.Fatal("__TESTROOT__ changed")
			}
			raw, err := os.ReadFile(generated)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), "remove-hidden-pages") || !strings.Contains(string(raw), `keepPages\":2`) {
				t.Fatalf("wrong runner command: %s", raw)
			}
			return nil, nil, nil
		}
		if args[0] != "xcodebuild" || !contains(args, "test-without-building") || !contains(args, "id=test-device") || !contains(args, generated) {
			t.Fatalf("bad cleanup argv: %v", args)
		}
		if _, err := os.Stat(generated); err != nil {
			t.Fatal(err)
		}
		return nil, nil, nil
	}
	if err := live.Cleanup(context.Background(), "remove-hidden-pages", 2); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("expected plutil read/write + XCTest, got %d calls", calls)
	}
	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Fatalf("temporary plist survived: %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("original runner was changed: %v", err)
	}
	if err := live.Cleanup(context.Background(), "uninstall", 1); err == nil {
		t.Fatal("accepted unsupported cleanup")
	}
	if err := live.Cleanup(context.Background(), "hide-other-pages", 0); err == nil {
		t.Fatal("accepted invalid page count")
	}
}

func TestCommandFailureHasPrivateEvidenceAndHonorsCancellation(t *testing.T) {
	live := fixtureLive(t)
	live.Run = func(ctx context.Context, _ []string) ([]byte, []byte, error) {
		<-ctx.Done()
		return []byte("partial result"), []byte("failed"), ctx.Err()
	}
	_, err := live.command(context.Background(), time.Millisecond, "synthetic-command")
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("expected deadline error: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(live.EvidenceDir, "command-*.json"))
	if len(files) != 2 {
		t.Fatalf("expected intent and outcome, got %v", files)
	}
	var found bool
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "partial result") && strings.Contains(string(raw), "deadline exceeded") {
			found = true
		}
	}
	if !found {
		t.Fatal("partial output/cancellation was not logged")
	}
}

func TestLocalSimulationCopiesStateAndNeverInventsShortcutAppIDs(t *testing.T) {
	initial := Snapshot{Identity: map[string]string{"mode": "local"},
		Layout: []any{[]any{"phone"}, []any{[]any{"Work", []any{"mail", "chat"}}, "/", "https://example.test"}}}
	local := NewLocal(initial)
	if !reflect.DeepEqual(local.State.AppIDs, []string{"chat", "mail", "phone"}) {
		t.Fatal(local.State.AppIDs)
	}
	initial.Identity["mode"] = "changed"
	captured, err := local.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	captured.Layout[0].([]any)[0] = "changed"
	captured.Identity["mode"] = "changed"
	current, _ := local.Capture(context.Background())
	if current.Identity["mode"] != "local" || current.Layout[0].([]any)[0] != "phone" {
		t.Fatal("snapshot aliases mutable state")
	}
	staged := []any{[]any{"phone"}, []any{"mail"}, []any{"chat"}}
	if err = local.Place(context.Background(), staged, "unused"); err != nil {
		t.Fatal(err)
	}
	if err = local.Cleanup(context.Background(), "hide-other-pages", 1); err != nil {
		t.Fatal(err)
	}
	if err = local.Cleanup(context.Background(), "remove-hidden-pages", 1); err != nil {
		t.Fatal(err)
	}
	if len(local.State.Layout) != 2 || !reflect.DeepEqual(local.Calls, []string{"place", "hide-other-pages", "remove-hidden-pages"}) {
		t.Fatal(local.State, local.Calls)
	}
}

func TestCancelledCommandNeverStartsInjectedRunner(t *testing.T) {
	live := fixtureLive(t)
	live.Run = func(context.Context, []string) ([]byte, []byte, error) {
		t.Fatal("cancelled context started a command")
		return nil, nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := live.command(ctx, time.Minute, "cfgutil", "set-icon-layout"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation before execution: %v", err)
	}
}
