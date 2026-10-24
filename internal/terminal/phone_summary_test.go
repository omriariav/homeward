package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/homeward/internal/device"
	"github.com/omriariav/homeward/internal/readiness"
)

func fakePhoneChecker(t *testing.T, fail *bool, calls *int) readiness.Checker {
	t.Helper()
	tool := filepath.Join(t.TempDir(), "cfgutil")
	if err := os.WriteFile(tool, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tool)
	if err != nil {
		t.Fatal(err)
	}
	return readiness.Checker{Stat: func(string) (os.FileInfo, error) { return info, nil }, LookPath: func(string) (string, error) { return "/synthetic/xcrun", nil }, Glob: func(string) ([]string, error) { return []string{"synthetic-runner"}, nil }, Run: func(ctx context.Context, args []string) ([]byte, []byte, error) {
		*calls++
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "set-icon-layout") || strings.Contains(joined, "xcodebuild") {
			t.Fatal("readiness attempted mutation", args)
		}
		if strings.Contains(joined, "--find") {
			return []byte("/synthetic/devicectl"), nil, nil
		}
		if strings.Contains(joined, "get UDID") {
			if *fail {
				return nil, nil, errors.New("synthetic disconnect")
			}
			return []byte(`{"Output":{"0x1":{"UDID":"synthetic"}}}`), nil, nil
		}
		if strings.Contains(joined, "lockState") {
			for i, arg := range args {
				if arg == "--json-output" {
					return nil, nil, os.WriteFile(args[i+1], []byte(`{"info":{"outcome":"success"},"result":{"passcodeRequired":false}}`), 0600)
				}
			}
		}
		t.Fatal("unexpected readiness command", args)
		return nil, nil, nil
	}}
}
func TestPersistentPhoneHeaderRerendersAfterExplicitCheckAndFailure(t *testing.T) {
	s, input, out := testScreen(t)
	s.app.Options.Demo = false
	s.app.Options.Device = "synthetic"
	s.app.Options.ECID = "0x1"
	s.app.Preferences.Alignment = "standard"
	fail := false
	calls := 0
	s.app.ReadinessChecker = fakePhoneChecker(t, &fail, &calls)
	// Dashboard p check -> read disclosure -> fresh typed yes -> dashboard q.
	input.keys = []Key{runeKey('p'), {Name: "enter"}, runeKey('y'), {Name: "enter"}, runeKey('q')}
	if err := s.dashboard(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "Phone: Not checked") || !strings.Contains(text, "Phone: Connected · just checked") || !strings.Contains(text, "XCTest not verified") {
		t.Fatal(text)
	}
	if calls != 3 || len(s.app.Device.(*device.Local).Calls) > 0 {
		t.Fatal(calls)
	}
	fail = true
	out.Reset()
	input.keys = []Key{runeKey('p'), {Name: "enter"}, runeKey('y'), {Name: "enter"}, {Name: "enter"}, runeKey('q')}
	if err := s.dashboard(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Read failed / unreachable") {
		t.Fatal("old connected status survived failed check", out.String())
	}
}
func TestPhoneSummaryIsPureAndProposalApprovalIsSeparate(t *testing.T) {
	app, _ := testApp(t)
	app.Options.Demo = false
	app.Options.Device = "synthetic"
	app.Options.ECID = "0x1"
	app.Preferences.Alignment = "standard"
	calls := 0
	app.ReadinessChecker.Run = func(context.Context, []string) ([]byte, []byte, error) { calls++; return nil, nil, nil }
	status := app.PhoneSummary(time.Now())
	if status.Connection != "Not checked" || status.Eligibility != "Unverified" || calls != 0 {
		t.Fatal(status, calls)
	}
	ready(t, app)
	status = app.PhoneSummary(time.Now())
	if status.Eligibility != "Unverified" || calls != 0 {
		t.Fatal("creating/reviewing proposal changed phone readiness", status, calls)
	}
	app.Preferences.Alignment = "bottom"
	if status = app.PhoneSummary(time.Now()); status.Eligibility != "Blocked" {
		t.Fatal(status)
	}
}
func TestPhoneStatusHintsRemainVisibleAtMinimumWidth(t *testing.T) {
	s, input, _ := testScreen(t)
	input.width, input.height = 56, 20
	s.app.Options.Demo = false
	s.app.Options.Device = "synthetic"
	s.app.Options.ECID = "0x1"
	s.app.Readiness = &readiness.Report{Rows: []readiness.Row{{Key: "cfgutil", Status: readiness.NeedsSetup, Detail: "Configurator is missing"}}}
	lines := s.phoneStatusLines()
	if !strings.HasSuffix(lines[0].text, "p Check") || !strings.HasSuffix(lines[1].text, "r Details") {
		t.Fatal(lines)
	}
	for _, line := range lines {
		if visibleWidth(line.text) > 55 {
			t.Fatal("status clips controls", line.text)
		}
	}
}
func TestCaptureFailureClearsConnectedSummary(t *testing.T) {
	app := physicalSession(t)
	local := app.Device.(*device.Local)
	target := &captureFailure{Local: local}
	app.Device = target
	app.Readiness = &readiness.Report{Checked: true, CheckedAt: time.Now().UTC().Format(time.RFC3339), Rows: []readiness.Row{{Key: "identity", Status: readiness.Ready}, {Key: "unlocked", Status: readiness.Ready}}}
	answers(app, "y")
	if err := app.Capture(context.Background()); err == nil {
		t.Fatal("expected capture error")
	}
	if strings.Contains(app.PhoneSummary(time.Now()).Connection, "Connected") {
		t.Fatal("failed capture retained old connection")
	}
}

type captureFailure struct{ *device.Local }

func (d *captureFailure) Capture(context.Context) (device.Snapshot, error) {
	return device.Snapshot{}, errors.New("synthetic read failure")
}
