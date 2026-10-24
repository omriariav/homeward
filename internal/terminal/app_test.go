package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/homeward/internal/core"
	"github.com/omriariav/homeward/internal/device"
)

func testApp(t *testing.T) (*App, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	ui := NewTerminal(strings.NewReader(""), out, false)
	app, err := New(Options{Root: t.TempDir(), OutputDir: t.TempDir(), Demo: true}, ui, nil)
	if err != nil {
		t.Fatal(err)
	}
	return app, out
}
func answers(app *App, values ...string) {
	i := 0
	app.UI.Read = func(prompt string) (string, error) {
		if i >= len(values) {
			return "", io.EOF
		}
		value := values[i]
		i++
		return value, nil
	}
}
func ready(t *testing.T, app *App) {
	t.Helper()
	app.Request = "Work calendar mail books"
	app.Preferences.Alignment = "standard"
	app.Preferences.AcceptReflow = true
	if err := app.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestDemoEndToEndApprovalIsTheOnlyMutation(t *testing.T) {
	app, out := testApp(t)
	steps := []string{"3", "Work mail calendar books", "4", "1", "item-0007", "", "standard", "none", "yes", "5", "Work mail calendar", "Books", "Social games", "6", "8"}
	index := 0
	app.UI.Read = func(prompt string) (string, error) {
		if strings.HasPrefix(prompt, "Type APPLY ") {
			if len(app.Device.(*device.Local).Calls) != 0 {
				t.Fatal("mutation before exact approval")
			}
			return app.Review.Challenge(), nil
		}
		if index < len(steps) {
			s := steps[index]
			index++
			return s, nil
		}
		return "0", nil
	}
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "SIMULATED VERIFIED") {
		t.Fatal(out.String())
	}
	if len(app.Device.(*device.Local).Calls) != 3 {
		t.Fatal(app.Device.(*device.Local).Calls)
	}
}
func TestDeclineAndEOFNeverMutate(t *testing.T) {
	for _, answer := range []string{"cancel", "eof"} {
		t.Run(answer, func(t *testing.T) {
			app, _ := testApp(t)
			ready(t, app)
			if answer == "eof" {
				answers(app)
			} else {
				answers(app, answer)
			}
			_ = app.Apply(context.Background())
			if len(app.Device.(*device.Local).Calls) != 0 {
				t.Fatal("changed before approval")
			}
		})
	}
}
func TestRevisionAndAbandonedReviewInvalidateApproval(t *testing.T) {
	app, _ := testApp(t)
	ready(t, app)
	old := app.Review.Challenge()
	answers(app, "Different priorities")
	if err := app.Context(); err != nil {
		t.Fatal(err)
	}
	if app.Review != nil || app.Reviewed != "" {
		t.Fatal("context revision retained approval")
	}
	answers(app, old)
	if err := app.Apply(context.Background()); err == nil {
		t.Fatal("applied edited context")
	}
	ready(t, app)
	app.UI.Paginate = true
	answers(app, "q")
	if err := app.View(); err != nil {
		t.Fatal(err)
	}
	if app.Reviewed != "" {
		t.Fatal("partial review authorized apply")
	}
	if err := app.Apply(context.Background()); err == nil {
		t.Fatal("accepted partial review")
	}
}
func TestAIRefusalAndEachRequestConsent(t *testing.T) {
	app, _ := testApp(t)
	app.RefusedAI = false
	app.Options.Demo = false
	app.Request = "Work mail"
	calls := 0
	app.AI.Runner = func(ctx context.Context, command []string, input, directory string) (string, error) {
		calls++
		result := map[string]any{"subtype": "success", "structured_output": map[string]any{"summary": "Focus", "questions": []any{}}}
		raw, _ := json.Marshal(result)
		return string(raw), nil
	}
	answers(app, "ai", "yes")
	if err := app.Questionnaire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	answers(app, "ai", "")
	if err := app.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !app.RefusedAI {
		t.Fatal("planning reused interview consent")
	}
	answers(app)
	if err := app.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("refusal did not persist")
	}
}
func TestSkippedAndInterruptedInterviewNeverInventAnswers(t *testing.T) {
	app, _ := testApp(t)
	app.Request = "Work focus"
	answers(app, "", "Books")
	err := app.Questionnaire(context.Background())
	if err != io.EOF {
		t.Fatal(err)
	}
	if app.Interview.Questions[0].Status != "skipped" || app.Interview.Questions[0].Answer != nil {
		t.Fatal(app.Interview)
	}
	if err = app.Generate(context.Background()); err == nil {
		t.Fatal("planned from pending answers")
	}
	files, _ := filepath.Glob(filepath.Join(app.Directory, "*-interview.json"))
	if len(files) < 2 {
		t.Fatal("answers not saved")
	}
}
func TestUntrustedLabelsNeverEmitTerminalControls(t *testing.T) {
	app, out := testApp(t)
	app.Metadata.Apps["demo.mail"] = core.AppMetadata{Label: "\x1b[2JMail\a\u202eFAKE"}
	ready(t, app)
	if strings.ContainsAny(out.String(), "\x1b\a\u202e") {
		t.Fatal("control injection")
	}
}
func TestLiveStartupDoesNotCallDevice(t *testing.T) {
	root := t.TempDir()
	out := &bytes.Buffer{}
	live := device.NewLive(root, "fake", "0x1", t.TempDir(), "")
	calls := 0
	live.Run = func(context.Context, []string) ([]byte, []byte, error) { calls++; return nil, nil, nil }
	app, err := New(Options{Root: root, OutputDir: t.TempDir(), Device: "fake", ECID: "0x1"}, NewTerminal(strings.NewReader("0\n"), out, false), live)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("startup contacted device")
	}
}
func TestSavedReviewAndDraftFilesArePrivate(t *testing.T) {
	app, _ := testApp(t)
	ready(t, app)
	paths, _ := filepath.Glob(filepath.Join(app.Directory, "*.json"))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(path, err)
		}
	}
}

func TestDemoMenuOneResetsWithoutFilePrompt(t *testing.T) {
	app, out := testApp(t)
	answers(app, "1", "3", "Work mail", "0")
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if app.Request != "Work mail" {
		t.Fatal("reset consumed the next menu choice as a file path")
	}
	if !strings.Contains(out.String(), "Synthetic demo reset") || !strings.Contains(out.String(), "Layout loaded. Start with 3") {
		t.Fatal(out.String())
	}
}
