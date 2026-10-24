package terminal

import (
	"fmt"
	"strings"

	"github.com/omriariav/homeward/internal/core"
	"github.com/omriariav/homeward/internal/organizer"
)

type proposalSection struct {
	title string
	lines []screenLine
}
type coverage struct{ seen [][]bool }

func (c *coverage) reset(sections []proposalSection) {
	c.seen = make([][]bool, len(sections))
	for i, section := range sections {
		c.seen[i] = make([]bool, len(section.lines))
	}
}
func (c *coverage) mark(section, start, end int) {
	for i := max(0, start); i < min(end, len(c.seen[section])); i++ {
		c.seen[section][i] = true
	}
}
func (c *coverage) counts() (read, total, complete int) {
	for _, section := range c.seen {
		all := true
		for _, seen := range section {
			total++
			if seen {
				read++
			} else {
				all = false
			}
		}
		if all {
			complete++
		}
	}
	return
}
func (c *coverage) next() (section, offset int, done bool) {
	for i, lines := range c.seen {
		for n, seen := range lines {
			if !seen {
				return i, n, false
			}
		}
	}
	return 0, 0, true
}

func proposalSections(d organizer.Document, width int, cleanup bool) []proposalSection {
	width = max(20, width)
	p := d.Artifact.Plan
	state := d.Artifact.Input.CurrentState
	items := map[string]core.Item{}
	for _, item := range state.Items {
		items[item.ID] = item
	}
	sections := []proposalSection{}
	add := func(title string, raw []string) {
		section := proposalSection{title: title}
		for _, text := range raw {
			for _, part := range wrap(text, width-4) {
				section.lines = append(section.lines, line(part))
			}
		}
		if len(section.lines) == 0 {
			section.lines = []screenLine{dim("None.")}
		}
		sections = append(sections, section)
	}
	selected := len(p.Dock)
	for _, page := range p.Pages {
		selected += len(page.Items)
	}
	prefs := d.Preferences
	alignment := prefs.Alignment
	if alignment == "preserve" {
		alignment = "Preserve current — manual only"
	}
	space := prefs.WidgetSpace
	if space == "" {
		space = "None"
	}
	ack := "No"
	if prefs.AcceptReflow {
		ack = "Yes"
	}
	overview := []string{p.Summary, "", fmt.Sprintf("%s · %d selected apps · %d off Home Screen", plural(len(p.Pages), "page", "pages"), selected, len(p.Hidden)), "No folders. No apps will be uninstalled.", "", "Your context: " + d.Artifact.Input.Request, "", fmt.Sprintf("Page budget: %d", prefs.MaxPages), "Alignment: " + alignment, "Manual widget space: " + space, "Accept widget loss / space reflow: " + ack}
	if d.Snapshot.Identity["udid"] != "" {
		overview = append(overview, "Target UDID: "+d.Snapshot.Identity["udid"], "Target ECID: "+d.Snapshot.Identity["ecid"])
	} else {
		overview = append(overview, "Target: local simulation only")
	}
	add("Overview", overview)
	grid := func(title, reason string, ids []string) {
		raw := []string{}
		if reason != "" {
			raw = append(raw, reason, "")
		}
		cellWidth := (width - 10) / 4
		edge := "+" + strings.Repeat(strings.Repeat("─", cellWidth)+"+", 4)
		raw = append(raw, edge)
		for start := 0; start < len(ids); start += 4 {
			row := "│"
			for col := 0; col < 4; col++ {
				label := ""
				if start+col < len(ids) {
					item := items[ids[start+col]]
					label = fmt.Sprintf("%d %s", start+col+1, item.Label)
				}
				row += pad(label, cellWidth) + "│"
			}
			raw = append(raw, row, edge)
		}
		if len(ids) == 0 {
			raw = append(raw, "(empty)")
		}
		raw = append(raw, "", "FULL APP NAMES")
		for n, id := range ids {
			item := items[id]
			label := item.Label
			if item.Kind == "widget" {
				label = "[widget] " + label
			}
			raw = append(raw, fmt.Sprintf("%d. %s", n+1, label))
			duplicates := 0
			for _, other := range state.Items {
				if other.Label == item.Label {
					duplicates++
				}
			}
			if duplicates > 1 {
				raw = append(raw, "   From "+source(item))
				if len(item.SourcePath) > 0 {
					raw = append(raw, fmt.Sprintf("   Original container position %d", item.SourcePath[len(item.SourcePath)-1]+1))
				}
			}
		}
		raw = append(raw, "", "Order preview only. Empty slots and widget geometry are not represented.")
		add(title, raw)
	}
	grid("Dock", "Your most accessible apps, in this order.", p.Dock)
	for n, page := range p.Pages {
		grid(fmt.Sprintf("Page %d · %s", n+1, page.Title), page.Reason, page.Items)
	}
	changes := []string{}
	groups := [][]string{p.Dock}
	for _, page := range p.Pages {
		groups = append(groups, page.Items)
	}
	for page, ids := range groups {
		for slot, id := range ids {
			item := items[id]
			if item.SourcePage != page || len(item.SourcePath) != 2 || item.SourcePath[len(item.SourcePath)-1] != slot || len(item.SourceFolders) > 0 {
				dest := fmt.Sprintf("Page %d", page)
				if page == 0 {
					dest = "Dock"
				}
				changes = append(changes, fmt.Sprintf("%s: %s → %s, slot %d", item.Label, source(item), dest, slot+1), "")
			}
		}
	}
	if len(changes) == 0 {
		changes = []string{"No icon moves or folder changes."}
	}
	add("Changes", changes)
	exclusions := []string{"These apps stay installed. Unresolved shortcuts require manual handling.", ""}
	if len(p.Hidden) == 0 {
		exclusions = append(exclusions, "Every supplied item remains visible.")
	}
	for _, entry := range p.Hidden {
		item := items[entry.Item]
		exclusions = append(exclusions, item.Label+" — "+entry.Reason, "From "+source(item), "")
	}
	add(fmt.Sprintf("Off Home Screen · %d", len(p.Hidden)), exclusions)
	notes := []string{"ASSUMPTIONS"}
	if len(p.Assumptions) == 0 {
		notes = append(notes, "None stated.")
	} else {
		notes = append(notes, p.Assumptions...)
	}
	notes = append(notes, "", "OPEN QUESTIONS")
	if len(p.Questions) == 0 {
		notes = append(notes, "None stated.")
	} else {
		notes = append(notes, p.Questions...)
	}
	notes = append(notes, "", "LIMITATIONS")
	notes = append(notes, state.Warnings...)
	notes = append(notes, d.Artifact.Warnings...)
	review, err := organizer.NewReview(d.Snapshot, d.Artifact, d.Preferences)
	if err == nil {
		problems := organizer.Blockers(review, cleanup)
		if len(problems) > 0 {
			notes = append(notes, "", "AUTOMATIC APPLY UNAVAILABLE")
			notes = append(notes, problems...)
		}
	}
	notes = append(notes, "", "Manual guide: preserve shortcuts, arrange bottom positions and empty spaces by hand, then add widgets. Exports are reconstruction aids, not exact backups.", "Other installed apps are staged temporarily and returned to App Library. No uninstallation.")
	add("Notes & limitations", notes)
	return sections
}
func (s *screen) proposal() (bool, error) {
	if s.app.Review == nil {
		return false, fmt.Errorf("create a proposal first")
	}
	document := s.app.Review.Data()
	section, offset := 0, 0
	lastWidth := 0
	var seen coverage
	for {
		w, _ := s.size()
		sections := proposalSections(document, w, s.app.Device.CleanupAvailable())
		if w != lastWidth {
			seen.reset(sections)
			lastWidth = w
		}
		section = min(section, len(sections)-1)
		current := sections[section]
		rows := s.rows()
		offset = min(offset, max(0, len(current.lines)-rows))
		read, total, complete := seen.counts()
		subtitle := fmt.Sprintf("%d / %d sections read · %d / %d lines", complete, len(sections), read, total)
		fits := s.paint(fmt.Sprintf("Proposal  %d/%d  ·  %s", section+1, len(sections), current.title), subtitle, current.lines[offset:min(len(current.lines), offset+rows)], "←→ section  ↑↓ scroll  Enter next / finish  Esc back")
		if fits {
			seen.mark(section, offset, min(len(current.lines), offset+rows))
		}
		k, err := s.key()
		if err != nil {
			return false, err
		}
		if isBack(k) {
			return false, nil
		}
		if !fits {
			continue
		}
		if k.Name == "right" || isRune(k, 'l') {
			section = (section + 1) % len(sections)
			offset = 0
		}
		if k.Name == "left" || isRune(k, 'h') {
			section = (section + len(sections) - 1) % len(sections)
			offset = 0
		}
		if down(k) {
			offset = min(offset+1, max(0, len(current.lines)-rows))
		}
		if up(k) {
			offset = max(0, offset-1)
		}
		if k.Name == "pagedown" || k.Name == "space" {
			offset = min(offset+rows, max(0, len(current.lines)-rows))
		}
		if k.Name == "pageup" {
			offset = max(0, offset-rows)
		}
		if k.Name == "home" {
			offset = 0
		}
		if k.Name == "end" {
			offset = max(0, len(current.lines)-rows)
		}
		if k.Name == "enter" {
			next, unread, done := seen.next()
			if done {
				return true, nil
			}
			section = next
			offset = unread
		}
	}
}
