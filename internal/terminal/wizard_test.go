package terminal

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/homeward/internal/device"
)

func TestWizardGuidesCreateReviewButNeverAutoApplies(t *testing.T) {
	s, input, out := testScreen(t)
	queue := []Key{runeKey('1'), {Name: "enter"}, {Name: "enter"}}
	queue = append(queue, typed("Work mail books")...)
	queue = append(queue, Key{Name: "enter"}, Key{Name: "enter"}, Key{Name: "down"}, Key{Name: "enter"}, Key{Name: "down"}, Key{Name: "enter"}, Key{Name: "enter"})
	steps := 0
	input.onRead = func() (Key, error) {
		steps++
		if steps > 180 {
			t.Fatal("wizard did not finish")
		}
		if len(queue) > 0 {
			k := queue[0]
			queue = queue[1:]
			return k, nil
		}
		if s.app.Mode == "custom" {
			return runeKey('q'), nil
		}
		return Key{Name: "enter"}, nil
	}
	if err := runScreen(context.Background(), s.app, input, out, false); err != nil {
		t.Fatal(err)
	}
	if s.app.Reviewed == "" || s.app.Artifact == nil || !s.app.InterviewSkipped {
		t.Fatal("guided creation/review did not complete")
	}
	if len(s.app.Device.(*device.Local).Calls) > 0 {
		t.Fatal("Wizard Next applied a proposal")
	}
	for _, text := range []string{"Choose your mode", "CREATE PROPOSAL", "Optional Apply", "Finish without applying"} {
		if !strings.Contains(out.String(), text) {
			t.Fatal("missing wizard cue", text)
		}
	}
}
func TestModesAndWizardBackPreserveForm(t *testing.T) {
	s, input, _ := testScreen(t)
	ready(t, s.app)
	s.app.WizardStep = 2
	request := s.app.Request
	prefs := s.app.Preferences
	review := s.app.Review.Digest()
	input.keys = []Key{runeKey('1'), {Name: "esc"}, runeKey('c'), runeKey('w'), runeKey('q')}
	if err := runScreen(context.Background(), s.app, input, s.output, false); err != nil {
		t.Fatal(err)
	}
	if s.app.WizardStep != 1 || s.app.Request != request || !reflect.DeepEqual(s.app.Preferences, prefs) || s.app.Review.Digest() != review {
		t.Fatal("mode/back navigation lost form state")
	}
}
func TestMissingProposalRoutesBackToCreate(t *testing.T) {
	s, input, out := testScreen(t)
	s.app.WizardStep = 5
	input.keys = []Key{{Name: "enter"}, runeKey('q')}
	if err := s.wizard(); err != nil {
		t.Fatal(err)
	}
	if s.app.WizardStep != 4 || s.app.Artifact != nil {
		t.Fatal("review created/implied a proposal")
	}
	if !strings.Contains(out.String(), "CREATE PROPOSAL") {
		t.Fatal("prerequisite not explained")
	}
}
func TestEscapedContextDraftSurvivesModesAndResumeWithoutSubmission(t *testing.T) {
	s, input, _ := testScreen(t)
	old := s.app.Request
	s.app.UI.InputKey = "context"
	input.keys = append(typed("Work and books"), Key{Name: "esc"})
	if _, err := s.ask("Context: "); !errors.Is(err, errBack) {
		t.Fatal(err)
	}
	if s.app.Request != old || s.app.FormDrafts["context"] != "Work and books" {
		t.Fatal("draft was submitted or lost")
	}
	resumed, err := resumeApp(t, s.app, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.app = resumed
	s.app.UI.InputKey = "context"
	input.keys = []Key{{Name: "enter"}}
	text, err := s.ask("Context: ")
	if err != nil || text != "Work and books" {
		t.Fatal(text, err)
	}
}
func TestOfflineReadinessHasNoDeviceCallsAndDistinguishesWidgets(t *testing.T) {
	s, input, out := testScreen(t)
	calls := 0
	s.app.ReadinessChecker.Run = func(context.Context, []string) ([]byte, []byte, error) { calls++; return nil, nil, nil }
	input.keys = []Key{runeKey('c'), {Name: "enter"}, runeKey('q')}
	if err := s.readiness(); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || len(s.app.Device.(*device.Local).Calls) > 0 {
		t.Fatal("offline readiness contacted a device")
	}
	text := out.String()
	if !strings.Contains(text, "DEMO: simulation only") || !strings.Contains(text, "Wizard guided workflow") {
		t.Fatal(text)
	}
}
func TestOriginalArtLeavesChoicesVisibleAtSupportedSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {56, 20}} {
		s, input, out := testScreen(t)
		input.width, input.height = size[0], size[1]
		input.keys = []Key{runeKey('q')}
		_, err := s.modeChooser()
		if !errors.Is(err, errQuit) {
			t.Fatal(err)
		}
		face := ".-^-."
		if size[1] < 24 {
			face = "'(__)'"
		}
		for _, text := range []string{face, "Phone:", "Eligibility:", "p Check", "r Details", "Wizard", "Custom", "Session / resume help", "Esc / q quit"} {
			if !strings.Contains(out.String(), text) {
				t.Fatalf("%v missing %q", size, text)
			}
		}
	}
}
