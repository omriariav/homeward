package terminal

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/omriariav/homeward/internal/core"
)

// Safe prevents app labels, device output and model responses emitting terminal
// escape sequences or hidden control characters. Callers preserve explicit lines.
func Safe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, s)
}
func cell(s string, n int) string {
	s = Safe(s)
	r := []rune(s)
	if len(r) > n {
		r = append(r[:n-1], '…')
	}
	return string(r) + strings.Repeat(" ", max(0, n-utf8.RuneCountInString(string(r))))
}
func source(item core.Item) string {
	where := fmt.Sprintf("Page %d", item.SourcePage)
	if item.SourcePage == 0 {
		where = "Dock"
	}
	if item.Kind == "widget" {
		where = "Widget location unknown"
	}
	if len(item.SourceFolders) > 0 {
		where += " / " + strings.Join(item.SourceFolders, " / ")
	}
	return where
}

func Render(artifact core.Artifact) (string, error) {
	if artifact.Status != "draft" {
		return "", fmt.Errorf("preview requires a completed draft")
	}
	p := artifact.Input
	if err := core.ValidatePlanWithAllowed(artifact.Plan, p.CurrentState, p.Selection, p.MaxPages, p.RequiredVisible, p.AllowedHidden); err != nil {
		return "", err
	}
	plan := artifact.Plan
	items := map[string]core.Item{}
	for _, item := range p.CurrentState.Items {
		items[item.ID] = item
	}
	var b strings.Builder
	fmt.Fprintln(&b, "PROPOSED HOME SCREEN — NOT APPLIED")
	fmt.Fprintln(&b, "Compared with the saved planning snapshot, not a live device read.")
	fmt.Fprintln(&b, Safe(plan.Summary))
	selected := len(plan.Dock)
	for _, page := range plan.Pages {
		selected += len(page.Items)
	}
	fmt.Fprintf(&b, "Pages: %d | Selected placements: %d | Off Home Screen: %d | Folders: 0\n", len(plan.Pages), selected, len(plan.Hidden))
	fmt.Fprintln(&b, "No apps will be uninstalled. Shortcut and widget reconstruction is not guaranteed.")
	label := func(id string) string {
		item := items[id]
		name := item.Label
		if name == "" {
			name = id
		}
		if item.Kind == "widget" {
			name = "[widget] " + name
		}
		return Safe(name)
	}
	grid := func(title, reason string, ids []string) {
		fmt.Fprintf(&b, "\n%s\n", Safe(title))
		if reason != "" {
			fmt.Fprintln(&b, Safe(reason))
		}
		if len(ids) == 0 {
			fmt.Fprintln(&b, "  (empty)")
		}
		for i, id := range ids {
			if i%4 == 0 {
				fmt.Fprint(&b, "|")
			}
			fmt.Fprint(&b, cell(fmt.Sprintf("%d. %s", i+1, label(id)), 20), "|")
			if i%4 == 3 || i == len(ids)-1 {
				fmt.Fprintln(&b)
			}
		}
		for i, id := range ids {
			fmt.Fprintf(&b, "  %d. %s [%s]\n", i+1, label(id), Safe(id))
		}
	}
	currentDock := []string{}
	for _, item := range p.CurrentState.Items {
		if item.SourcePage == 0 && item.Kind == "icon" {
			currentDock = append(currentDock, label(item.ID))
		}
	}
	fmt.Fprintln(&b, "Current Dock: "+strings.Join(currentDock, " | "))
	grid("Proposed Dock", "", plan.Dock)
	for n, page := range plan.Pages {
		grid(fmt.Sprintf("Page %d: %s", n+1, page.Title), page.Reason, page.Items)
	}
	fmt.Fprintln(&b, "\nMOVES / FOLDER CHANGES")
	groups := [][]string{plan.Dock}
	for _, page := range plan.Pages {
		groups = append(groups, page.Items)
	}
	changes := 0
	for page, ids := range groups {
		for slot, id := range ids {
			item := items[id]
			if item.SourcePage != page || len(item.SourcePath) != 2 || item.SourcePath[len(item.SourcePath)-1] != slot || len(item.SourceFolders) > 0 {
				dest := fmt.Sprintf("Page %d", page)
				if page == 0 {
					dest = "Dock"
				}
				fmt.Fprintf(&b, "%s [%s]: %s → %s, slot %d\n", label(id), id, Safe(source(item)), dest, slot+1)
				changes++
			}
		}
	}
	if changes == 0 {
		fmt.Fprintln(&b, "  None.")
	}
	fmt.Fprintln(&b, "\nOFF HOME SCREEN — REVIEW EXCLUSIONS")
	if len(plan.Hidden) == 0 {
		fmt.Fprintln(&b, "  None.")
	}
	for _, hidden := range plan.Hidden {
		fmt.Fprintf(&b, "%s [%s] — %s (from %s)\n", label(hidden.Item), hidden.Item, Safe(hidden.Reason), Safe(source(items[hidden.Item])))
	}
	for _, section := range []struct {
		title string
		lines []string
	}{{"ASSUMPTIONS", plan.Assumptions}, {"OPEN QUESTIONS", plan.Questions}, {"LIMITATIONS", append(append([]string{}, p.CurrentState.Warnings...), artifact.Warnings...)}} {
		if len(section.lines) > 0 {
			fmt.Fprintln(&b, "\n"+section.title)
			for _, line := range section.lines {
				fmt.Fprintln(&b, "- "+Safe(line))
			}
		}
	}
	fmt.Fprintln(&b, "\nIcon order only: widget dimensions, free positions and reserved spaces are not simulated.")
	fmt.Fprintln(&b, "Read-only preview. Use the interactive workflow for separate reviewed-plan Apply confirmation.")
	return b.String(), nil
}
