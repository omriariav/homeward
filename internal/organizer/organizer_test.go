package organizer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/homeward/internal/core"
	"github.com/omriariav/homeward/internal/device"
)

func fixture(t *testing.T) (Review, *device.Local) {
	t.Helper()
	local := device.NewLocal(device.Snapshot{Identity: map[string]string{"udid": "synthetic", "ecid": "0x1"}, Layout: []any{[]any{"phone"}, []any{"mail", "games", "books"}}, AppIDs: []string{"phone", "mail", "games", "books", "library-only"}, StageIDs: []string{"phone", "mail", "games", "books", "library-only"}})
	snapshot, err := local.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state, err := core.Normalize(snapshot.Layout, core.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	artifact := core.Artifact{Version: 1, Status: "draft", Input: core.Input{Selection: "curate", MaxPages: 1, CurrentState: state}, Plan: core.Plan{Summary: "Mail first", Dock: []string{"item-0001"}, Pages: []core.Page{{Title: "Work", Reason: "Mail", Items: []string{"item-0002"}}}, Hidden: []core.Exclusion{{Item: "item-0003", Reason: "Lower priority"}, {Item: "item-0004", Reason: "Lower priority"}}}}
	review, err := NewReview(snapshot, artifact, core.Preferences{MaxPages: 1, Alignment: "standard", AcceptReflow: true})
	if err != nil {
		t.Fatal(err)
	}
	return review, local
}
func TestApprovalAndRevisionsFailBeforeDeviceWork(t *testing.T) {
	review, local := fixture(t)
	doc := review.Data()
	doc.Preferences.MaxPages = 2
	newReview, err := NewReview(doc.Snapshot, doc.Artifact, doc.Preferences)
	if err != nil {
		t.Fatal(err)
	}
	for _, approval := range []string{"", "yes", review.Challenge()} {
		_, err := Execute(context.Background(), newReview, approval, local, filepath.Join(t.TempDir(), "apply"))
		if err == nil {
			t.Fatal("accepted missing/stale approval")
		}
	}
	if len(local.Calls) != 0 {
		t.Fatal("mutated before approval")
	}
	if review.Data().Preferences.MaxPages != 1 {
		t.Fatal("review data is mutable")
	}
}
func TestSuccessfulPipelineStagesLibraryAppsAndVerifies(t *testing.T) {
	review, local := fixture(t)
	dir := filepath.Join(t.TempDir(), "apply")
	actual, err := Execute(context.Background(), review, review.Challenge(), local, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(local.Calls, []string{"place", "hide-other-pages", "remove-hidden-pages"}) {
		t.Fatal(local.Calls)
	}
	expected, _ := SelectedLayout(review)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal(actual)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "staged.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "library-only") {
		t.Fatal("App Library app was omitted from explicit staging")
	}
	info, _ := os.Stat(filepath.Join(dir, "before.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}
func TestFreshStateMismatchRejectsBeforeMutation(t *testing.T) {
	for _, which := range []string{"layout", "device", "apps", "stage"} {
		t.Run(which, func(t *testing.T) {
			review, local := fixture(t)
			switch which {
			case "layout":
				local.State.Layout[1] = []any{"books", "games", "mail"}
			case "device":
				local.State.Identity["udid"] = "other"
			case "apps":
				local.State.AppIDs = append(local.State.AppIDs, "new")
			case "stage":
				local.State.StageIDs = append(local.State.StageIDs, "new")
			}
			_, err := Execute(context.Background(), review, review.Challenge(), local, filepath.Join(t.TempDir(), "apply"))
			if err == nil || len(local.Calls) > 0 {
				t.Fatalf("err=%v calls=%v", err, local.Calls)
			}
		})
	}
}
func TestUnsupportedGeometryAndShortcutsFailClosed(t *testing.T) {
	for _, which := range []string{"preserve", "bottom", "widgets", "ack", "shortcut", "duplicate", "metadata"} {
		t.Run(which, func(t *testing.T) {
			review, local := fixture(t)
			doc := review.Data()
			switch which {
			case "preserve", "bottom":
				doc.Preferences.Alignment = which
			case "widgets":
				doc.Preferences.WidgetSpace = "top two rows"
			case "ack":
				doc.Preferences.AcceptReflow = false
			case "shortcut":
				doc.Snapshot.Layout[1].([]any)[2] = "/"
				doc.Artifact.Input.CurrentState.Items[3].SourceID = "/"
			case "duplicate":
				doc.Snapshot.Layout[1].([]any)[2] = "mail"
				doc.Artifact.Input.CurrentState.Items[3].SourceID = "mail"
			case "metadata":
				doc.Artifact.Input.CurrentState.Items = append(doc.Artifact.Input.CurrentState.Items, core.Item{ID: "widget", Kind: "widget", Label: "Calendar", SourcePage: -1})
				doc.Artifact.Plan.Pages[0].Items = append(doc.Artifact.Plan.Pages[0].Items, "widget")
			}
			review, err := NewReview(doc.Snapshot, doc.Artifact, doc.Preferences)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Execute(context.Background(), review, review.Challenge(), local, filepath.Join(t.TempDir(), "apply"))
			if err == nil || len(local.Calls) > 0 {
				t.Fatalf("err=%v calls=%v", err, local.Calls)
			}
		})
	}
}

type brokenDevice struct {
	*device.Local
	badStage, badFinal, failCleanup, noCleanup bool
	reads                                      int
}

func (d *brokenDevice) CleanupAvailable() bool { return !d.noCleanup }
func (d *brokenDevice) ReadLayout(ctx context.Context) ([]any, error) {
	d.reads++
	layout, err := d.Local.ReadLayout(ctx)
	if (d.badStage && d.reads == 1) || (d.badFinal && d.reads == 2) {
		layout = append(layout, []any{"unexpected"})
	}
	return layout, err
}
func (d *brokenDevice) Cleanup(ctx context.Context, action string, keep int) error {
	if d.failCleanup {
		return errors.New("runner failed")
	}
	return d.Local.Cleanup(ctx, action, keep)
}
func TestPartialFailureIsRecordedAndDoesNotContinueBlindly(t *testing.T) {
	for _, which := range []string{"stage", "cleanup", "final"} {
		t.Run(which, func(t *testing.T) {
			review, local := fixture(t)
			target := &brokenDevice{Local: local, badStage: which == "stage", failCleanup: which == "cleanup", badFinal: which == "final"}
			dir := filepath.Join(t.TempDir(), "apply")
			_, err := Execute(context.Background(), review, review.Challenge(), target, dir)
			if err == nil || !strings.Contains(err.Error(), "partially changed") {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(filepath.Join(dir, "result.json"))
			if !strings.Contains(string(raw), "partial-or-unknown") {
				t.Fatal(string(raw))
			}
			if which == "stage" && len(local.Calls) != 1 {
				t.Fatalf("cleanup followed bad staging: %v", local.Calls)
			}
		})
	}
}
func TestMissingCleanupAndCancelledContextNeverMutate(t *testing.T) {
	review, local := fixture(t)
	target := &brokenDevice{Local: local, noCleanup: true}
	_, err := Execute(context.Background(), review, review.Challenge(), target, filepath.Join(t.TempDir(), "apply"))
	if err == nil || len(local.Calls) > 0 {
		t.Fatal(err, local.Calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Execute(ctx, review, review.Challenge(), local, filepath.Join(t.TempDir(), "cancelled"))
	if err == nil || len(local.Calls) > 0 {
		t.Fatal(err, local.Calls)
	}
}
func TestSourceForgeryRejected(t *testing.T) {
	review, _ := fixture(t)
	doc := review.Data()
	doc.Artifact.Input.CurrentState.Items[1].SourceID = "other.app"
	if _, err := NewReview(doc.Snapshot, doc.Artifact, doc.Preferences); err == nil {
		t.Fatal("accepted changed source identifier")
	}
}
