package terminal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/homeward/internal/core"
	"github.com/omriariav/homeward/internal/device"
)

func resumeApp(t *testing.T, source *App, options Options, target device.Device) (*App, error) {
	t.Helper()
	options.Resume = source.Directory
	options.Root = t.TempDir()
	options.OutputDir = t.TempDir()
	return New(options, NewTerminal(strings.NewReader(""), os.Stderr, false), target)
}
func TestResumeRestoresDataNeverApprovalOrConsent(t *testing.T) {
	app, _ := testApp(t)
	ready(t, app)
	app.Mode = "wizard"
	app.WizardStep = 5
	app.FormDrafts["context"] = "An unsubmitted edit"
	if err := app.SaveSession(); err != nil {
		t.Fatal(err)
	}
	resumed, err := resumeApp(t, app, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Request != app.Request || resumed.Artifact == nil || resumed.Review == nil || resumed.WizardStep != 5 || resumed.FormDrafts["context"] != "An unsubmitted edit" {
		t.Fatal("saved form not restored")
	}
	if resumed.Reviewed != "" || resumed.Preferences.AcceptReflow || !resumed.RefusedAI {
		t.Fatal("approval/acknowledgement restored")
	}
	if resumed.Directory == app.Directory {
		t.Fatal("resuming overwrites old evidence")
	}
	if len(resumed.Device.(*device.Local).Calls) != 0 {
		t.Fatal("resume mutated a device")
	}
	raw, err := os.ReadFile(filepath.Join(resumed.Directory, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, key := range []string{"reviewed", "approval", "consent_ai", "root", "cfgutil"} {
		if _, ok := fields[key]; ok {
			t.Fatal("persisted authority", key)
		}
	}
	// The reset acknowledgement is synchronized with the stored draft policy, so
	// resuming a resumed session remains valid instead of trusting an old grant.
	if _, err = resumeApp(t, resumed, Options{}, nil); err != nil {
		t.Fatal(err)
	}
}
func TestResumeRejectsStaleContextAndSelectionPolicy(t *testing.T) {
	app, _ := testApp(t)
	ready(t, app)
	raw, err := os.ReadFile(filepath.Join(app.Directory, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, which := range []string{"context", "page_budget", "keep", "dock", "required", "tool_path", "approval"} {
		t.Run(which, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			input := value["artifact"].(map[string]any)["input"].(map[string]any)
			prefs := value["preferences"].(map[string]any)
			switch which {
			case "context":
				value["request"] = "Changed context"
			case "page_budget":
				prefs["max_pages"] = 1
			case "keep":
				prefs["keep"] = []any{"item-0001"}
			case "dock":
				prefs["dock"] = []any{}
			case "required":
				input["required_visible"] = []any{"item-0001"}
			case "tool_path":
				value["cfgutil"] = "/tmp/untrusted-tool"
			case "approval":
				value["approval"] = "APPLY saved"
			}
			path := filepath.Join(t.TempDir(), "session.json")
			encoded, _ := json.Marshal(value)
			_ = os.WriteFile(path, encoded, 0600)
			if _, err := loadSession(path); err == nil {
				t.Fatalf("accepted stale/untrusted %s", which)
			}
		})
	}
}
func physicalSession(t *testing.T) *App {
	t.Helper()
	app, _ := testApp(t)
	app.Options.Demo = false
	app.Options.Device = "synthetic-udid"
	app.Options.ECID = "0x123"
	app.Snapshot.Identity = map[string]string{"udid": "synthetic-udid", "ecid": "0x123"}
	app.Device = device.NewLocal(*app.Snapshot)
	app.Preferences.Keep = []string{"item-0007"}
	ready(t, app)
	if err := app.SaveSession(); err != nil {
		t.Fatal(err)
	}
	return app
}
func TestPhysicalResumeCannotEnableTransportFromFile(t *testing.T) {
	app := physicalSession(t)
	resumed, err := resumeApp(t, app, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.ResumeReadOnly || !resumed.Device.Simulated() || resumed.Options.Device != "" {
		t.Fatal("saved file enabled live transport")
	}
	if err = resumed.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatal(err)
	}
	if _, err = resumeApp(t, app, Options{Device: "another-phone", ECID: "0x123"}, nil); err == nil {
		t.Fatal("mismatched target accepted")
	}
	target := device.NewLocal(*app.Snapshot)
	resumed, err = resumeApp(t, app, Options{Device: "synthetic-udid", ECID: "0x123"}, target)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.NeedsCapture || resumed.Reviewed != "" {
		t.Fatal("saved live snapshot treated as fresh")
	}
	if err = resumed.Apply(context.Background()); err == nil || len(target.Calls) > 0 {
		t.Fatal("resume applied without recapture", err, target.Calls)
	}
}
func TestFreshRecapturePreservesSameFormAndInvalidatesChangedSource(t *testing.T) {
	app := physicalSession(t)
	target := device.NewLocal(*app.Snapshot)
	resumed, err := resumeApp(t, app, Options{Device: "synthetic-udid", ECID: "0x123"}, target)
	if err != nil {
		t.Fatal(err)
	}
	answers(resumed, "y")
	if err = resumed.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resumed.NeedsCapture || resumed.Artifact == nil || !reflect.DeepEqual(resumed.Preferences.Keep, app.Preferences.Keep) {
		t.Fatal("same-source form was lost")
	}
	target.State.Layout[1].([]any)[3] = "demo.music"
	answers(resumed, "y")
	if err = resumed.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resumed.Artifact != nil || len(resumed.Preferences.Keep) > 0 || resumed.Request != app.Request {
		t.Fatal("source-bound selections not invalidated, or context lost")
	}
	if len(target.Calls) > 0 {
		t.Fatal("capture mutated device")
	}
}
func TestResumeValidatesActualInterviewAnswers(t *testing.T) {
	app, _ := testApp(t)
	app.Request = "Work focus"
	answers(app, "Work", "Books", "")
	if err := app.Questionnaire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := app.CreateProposal(context.Background()); err != nil {
		t.Fatal(err)
	}
	resumed, err := resumeApp(t, app, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resumed.Interview, app.Interview) {
		t.Fatal("actual answers changed")
	}
	before := resumed.Interview.Questions[0].Answer
	answers(resumed)
	if err = resumed.Questionnaire(context.Background()); err != nil {
		t.Fatal("asked answered questions again", err)
	}
	if *resumed.Interview.Questions[0].Answer != *before {
		t.Fatal("saved answer replaced")
	}
	// This also proves pending questions can resume without another provider call.
	resumed.Interview.Questions = append(resumed.Interview.Questions, core.Question{ID: "extra", Question: "Anything else?", Why: "Priorities", Status: "pending"})
	resumed.invalidate()
	answers(resumed, "Maps")
	if err = resumed.Questionnaire(context.Background()); err != nil {
		t.Fatal(err)
	}
}
