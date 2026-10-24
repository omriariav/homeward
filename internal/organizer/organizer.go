// Package organizer binds explicit approval to an immutable, reviewed plan and
// implements the fail-closed Apply pipeline independently of the terminal UI.
package organizer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/omriariav/homeward/internal/core"
	"github.com/omriariav/homeward/internal/device"
)

type Document struct {
	Snapshot    device.Snapshot  `json:"snapshot"`
	Artifact    core.Artifact    `json:"artifact"`
	Preferences core.Preferences `json:"preferences"`
}

// Review has no mutable exported fields. Any revision requires a new review and
// a different confirmation; Data always returns a deep copy.
type Review struct{ document string }

func NewReview(snapshot device.Snapshot, artifact core.Artifact, preferences core.Preferences) (Review, error) {
	if artifact.Status != "draft" {
		return Review{}, errors.New("review requires a completed draft")
	}
	if preferences.MaxPages < 1 || preferences.MaxPages > 15 {
		return Review{}, errors.New("review page budget must be 1–15")
	}
	required := append(append(append([]string{}, artifact.Input.RequiredVisible...), preferences.Keep...), preferences.Dock...)
	if err := core.ValidatePlanWithAllowed(artifact.Plan, artifact.Input.CurrentState, artifact.Input.Selection, preferences.MaxPages, required, artifact.Input.AllowedHidden); err != nil {
		return Review{}, err
	}
	if preferences.Dock != nil && !reflect.DeepEqual(artifact.Plan.Dock, preferences.Dock) {
		return Review{}, errors.New("reviewed plan does not honor explicit Dock choices")
	}
	if artifact.Input.MaxPages > 0 && len(artifact.Plan.Pages) > artifact.Input.MaxPages {
		return Review{}, errors.New("plan exceeds its recorded page budget")
	}
	baseline, err := core.Normalize(snapshot.Layout, core.Metadata{})
	if err != nil {
		return Review{}, err
	}
	icons := []core.Item{}
	for _, item := range artifact.Input.CurrentState.Items {
		if item.Kind == "icon" {
			icons = append(icons, item)
		}
	}
	if len(icons) != len(baseline.Items) {
		return Review{}, errors.New("draft inventory does not match captured layout")
	}
	for i, item := range icons {
		base := baseline.Items[i]
		if item.ID != base.ID || item.SourceID != base.SourceID || item.SourcePage != base.SourcePage || !reflect.DeepEqual(item.SourcePath, base.SourcePath) || !reflect.DeepEqual(item.SourceFolders, base.SourceFolders) {
			return Review{}, errors.New("draft source identifiers or positions differ from captured layout")
		}
	}
	data, err := json.Marshal(Document{snapshot, artifact, preferences})
	if err != nil {
		return Review{}, err
	}
	return Review{string(data)}, nil
}
func (r Review) Data() Document { var d Document; _ = json.Unmarshal([]byte(r.document), &d); return d }
func (r Review) Digest() string {
	sum := sha256.Sum256([]byte(r.document))
	return hex.EncodeToString(sum[:])
}
func (r Review) Challenge() string { return "APPLY " + r.Digest()[:16] }

func Blockers(r Review, cleanupAvailable bool) []string {
	d := r.Data()
	p := d.Preferences
	out := []string{}
	if p.Alignment != "standard" {
		out = append(out, "Preserved/unknown or bottom alignment cannot be represented by cfgutil. Select standard sequential row order, or arrange icons manually.")
	}
	if strings.TrimSpace(p.WidgetSpace) != "" {
		out = append(out, "Reserved widget space is unsupported. Add widgets and arrange spaces manually; automatic Apply is blocked while a reservation is requested.")
	}
	if !p.AcceptReflow {
		out = append(out, "Bulk placement can remove EXISTING widgets and reflow empty space. Explicit acceptance is required.")
	}
	if !cleanupAvailable {
		out = append(out, "A built physical XCTest runner is required. Developer Mode, signing, trust and UI Automation must also work. cfgutil alone cannot reliably remove omitted apps.")
	}
	known := set(d.Snapshot.AppIDs)
	seen := map[string]bool{}
	unknown := []string{}
	for _, item := range d.Artifact.Input.CurrentState.Items {
		if item.Kind != "icon" {
			out = append(out, "Widget metadata cannot be executed or reconstructed. Use this proposal as a manual guide.")
			continue
		}
		if seen[item.SourceID] {
			out = append(out, "Repeated identifiers cannot be safely distinguished during bulk placement. Shortcut placeholders are not interchangeable.")
		}
		seen[item.SourceID] = true
		if (item.SourceID == "/" || strings.Contains(item.SourceID, "://")) || (len(known) > 0 && !known[item.SourceID]) {
			unknown = append(unknown, item.SourceID)
		}
	}
	if len(unknown) > 0 {
		out = append(out, "Protected unresolved shortcuts/non-app identifiers: "+strings.Join(unknown, ", ")+". Automatic Apply is blocked because reconstruction is not guaranteed. Keep them in place and use the proposal manually.")
	}
	return out
}
func set(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}
func sameStrings(a, b []string) bool {
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return reflect.DeepEqual(x, y)
}

func SelectedLayout(r Review) ([]any, error) {
	d := r.Data()
	items := map[string]core.Item{}
	for _, item := range d.Artifact.Input.CurrentState.Items {
		if item.Kind != "icon" {
			return nil, errors.New("widget geometry cannot be executed")
		}
		items[item.ID] = item
	}
	keys := [][]string{d.Artifact.Plan.Dock}
	for _, page := range d.Artifact.Plan.Pages {
		keys = append(keys, page.Items)
	}
	layout := make([]any, 0, len(keys))
	for _, page := range keys {
		row := make([]any, 0, len(page))
		for _, id := range page {
			item, ok := items[id]
			if !ok {
				return nil, errors.New("unknown plan item")
			}
			row = append(row, item.SourceID)
		}
		layout = append(layout, row)
	}
	return layout, nil
}

func StageLayout(expected []any, fresh device.Snapshot) ([]any, error) {
	state, err := core.Normalize(fresh.Layout, core.Metadata{})
	if err != nil {
		return nil, err
	}
	known := set(fresh.AppIDs)
	selected := map[string]bool{}
	extras := set(fresh.StageIDs)
	for _, item := range state.Items {
		if !known[item.SourceID] {
			return nil, errors.New("fresh inventory contains an unresolved shortcut/non-app identifier; no placement attempted")
		}
		extras[item.SourceID] = true
	}
	for _, raw := range expected {
		page, ok := raw.([]any)
		if !ok {
			return nil, errors.New("invalid expected page")
		}
		for _, value := range page {
			id, ok := value.(string)
			if !ok || !known[id] || selected[id] {
				return nil, errors.New("selected identifiers are ambiguous or absent from the fresh installed-app inventory")
			}
			selected[id] = true
			delete(extras, id)
		}
	}
	for id := range extras {
		if !known[id] {
			return nil, errors.New("staging catalog contains an unknown app")
		}
	}
	keys := make([]string, 0, len(extras))
	for id := range extras {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	data, _ := json.Marshal(expected)
	var staged []any
	_ = json.Unmarshal(data, &staged)
	for i := 0; i < len(keys); i += 24 {
		end := i + 24
		if end > len(keys) {
			end = len(keys)
		}
		page := []any{}
		for _, id := range keys[i:end] {
			page = append(page, id)
		}
		staged = append(staged, page)
	}
	if len(staged)-1 > 15 {
		return nil, errors.New("staging all observed apps needs more than 15 pages; automatic Apply is blocked")
	}
	return staged, nil
}

func SaveJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

// Execute is the only path to device mutation. Failures after the first placement
// attempt are explicitly partial/unknown, never successful and never auto-rolled back.
func Execute(ctx context.Context, r Review, approval string, target device.Device, directory string) (result []any, err error) {
	if approval != r.Challenge() {
		return nil, errors.New("exact reviewed-plan confirmation required; no device changes attempted")
	}
	d := r.Data()
	if _, err = NewReview(d.Snapshot, d.Artifact, d.Preferences); err != nil {
		return nil, err
	}
	if problems := Blockers(r, target.CleanupAvailable()); len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	expected, err := SelectedLayout(r)
	if err != nil {
		return nil, err
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		return nil, err
	}
	mutated := false
	phase := "preflight"
	defer func() {
		if err == nil {
			return
		}
		status := "not-applied"
		note := "No mutation command was sent."
		if mutated {
			status = "partial-or-unknown"
			note = "Device may be partially changed. Stop and inspect it; do not retry blindly. A device-side runner may still finish after cancellation. Saved layouts are reconstruction aids, not exact widget/shortcut backups."
		}
		logErr := SaveJSON(filepath.Join(directory, "result.json"), map[string]any{"status": status, "phase": phase, "error": err.Error(), "digest": r.Digest()})
		if logErr != nil {
			note += " Could not save failure result: " + logErr.Error()
		}
		err = fmt.Errorf("%s: %w. %s Evidence: %s", phase, err, note, directory)
	}()
	if err = SaveJSON(filepath.Join(directory, "review.json"), d); err != nil {
		return nil, err
	}
	if err = target.Preflight(ctx); err != nil {
		return nil, err
	}
	fresh, err := target.Capture(ctx)
	if err != nil {
		return nil, err
	}
	if err = SaveJSON(filepath.Join(directory, "before.json"), fresh); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(fresh.Identity, d.Snapshot.Identity) {
		return nil, errors.New("device identity changed; recapture and review again")
	}
	if !reflect.DeepEqual(fresh.Layout, d.Snapshot.Layout) {
		return nil, errors.New("device layout changed since review; recapture and regenerate. This comparison cannot detect widget or free-position geometry")
	}
	for key, pair := range map[string][2][]string{"app_ids": {d.Snapshot.AppIDs, fresh.AppIDs}, "stage_ids": {d.Snapshot.StageIDs, fresh.StageIDs}} {
		if pair[0] != nil && !sameStrings(pair[0], pair[1]) {
			return nil, fmt.Errorf("installed app inventory changed (%s); recapture and regenerate", key)
		}
	}
	staged, err := StageLayout(expected, fresh)
	if err != nil {
		return nil, err
	}
	if err = SaveJSON(filepath.Join(directory, "expected.json"), expected); err != nil {
		return nil, err
	}
	stagePath := filepath.Join(directory, "staged.json")
	if err = SaveJSON(stagePath, staged); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	phase = "cfgutil staging"
	mutated = true
	if err = target.Place(ctx, staged, stagePath); err != nil {
		return nil, err
	}
	actual, err := target.ReadLayout(ctx)
	if err != nil {
		return nil, err
	}
	if err = SaveJSON(filepath.Join(directory, "staged-readback.json"), actual); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(staged, actual) {
		return nil, errors.New("staging readback differs: extra, missing or reordered icons/pages; cleanup was not started")
	}
	if len(staged) > len(expected) {
		phase = "hide trailing pages"
		if err = target.Cleanup(ctx, "hide-other-pages", len(expected)-1); err != nil {
			return nil, err
		}
		phase = "remove hidden pages"
		if err = target.Cleanup(ctx, "remove-hidden-pages", len(expected)-1); err != nil {
			return nil, err
		}
	}
	phase = "final verification"
	actual, err = target.ReadLayout(ctx)
	if err != nil {
		return nil, err
	}
	if err = SaveJSON(filepath.Join(directory, "final-readback.json"), actual); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(expected, actual) {
		return nil, errors.New("final layout differs from reviewed plan; exact verification failed")
	}
	if err = SaveJSON(filepath.Join(directory, "result.json"), map[string]any{"status": "verified", "digest": r.Digest(), "simulated": target.Simulated()}); err != nil {
		return nil, err
	}
	return actual, nil
}
