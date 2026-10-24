package terminal

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/omriariav/homeward/internal/core"
)

func (s *screen) preferences() error {
	state, err := s.app.state()
	if err != nil {
		return err
	}
	selected := 0
	for {
		p := s.app.Preferences
		alignment := "Preserve current · manual only"
		if p.Alignment == "standard" {
			alignment = "Standard rows"
		} else if p.Alignment == "bottom" {
			alignment = "Bottom · manual only"
		}
		dock := "Keep current order"
		if p.Dock != nil {
			dock = fmt.Sprintf("%d chosen apps", len(p.Dock))
		}
		space := p.WidgetSpace
		if space == "" {
			space = "None requested"
		}
		ack := "Not accepted"
		if p.AcceptReflow {
			ack = "Accepted for standard rows"
		}
		labels := []string{fmt.Sprintf("Home pages          %d", p.MaxPages), fmt.Sprintf("Must-keep apps      %d selected", len(p.Keep)), "Dock                " + dock, "Icon alignment      " + alignment, "Widget space        " + space, "Widget / reflow      " + ack}
		body := []screenLine{dim("CONTENT")}
		for i, label := range labels {
			if i == 3 {
				body = append(body, line(""), dim("PLACEMENT"))
			}
			prefix := "  "
			tone := ""
			if i == selected {
				prefix = "› "
				tone = "selected"
			}
			body = append(body, screenLine{prefix + label, tone})
		}
		body = append(body, line(""), dim("Changes save immediately and invalidate the proposal."))
		fits := s.paint("Layout preferences", "A small shortlist. No folders. Your must-keeps come first.", body, "↑↓ move  Enter edit  ←→ pages  Esc done")
		k, err := s.key()
		if err != nil {
			return err
		}
		if isBack(k) {
			return nil
		}
		if !fits {
			continue
		}
		if down(k) {
			selected = (selected + 1) % len(labels)
			continue
		}
		if up(k) {
			selected = (selected + len(labels) - 1) % len(labels)
			continue
		}
		previous := p
		if selected == 0 && (k.Name == "left" || k.Name == "right") {
			if k.Name == "left" {
				p.MaxPages = max(1, p.MaxPages-1)
			} else {
				p.MaxPages = min(15, p.MaxPages+1)
			}
		} else if k.Name == "enter" {
			switch selected {
			case 0:
				choices := []string{}
				for i := 1; i <= 15; i++ {
					choices = append(choices, fmt.Sprintf("%d Home Screen pages", i))
				}
				n, e := s.choose("Page budget", "Dock does not count toward this limit.", choices, p.MaxPages-1)
				if e == errBack {
					continue
				}
				if e != nil {
					return e
				}
				p.MaxPages = n + 1
			case 1:
				ids, saved, e := s.pickApps(state, p.Keep, false)
				if e != nil {
					return e
				}
				if saved {
					p.Keep = ids
				}
			case 2:
				ids, saved, e := s.pickApps(state, p.Dock, true)
				if e != nil {
					return e
				}
				if saved {
					p.Dock = ids
				}
			case 3:
				names := []string{"Preserve current positions — manual only", "Standard sequential rows — no empty slots", "Bottom alignment — manual only"}
				index := 0
				if p.Alignment == "standard" {
					index = 1
				} else if p.Alignment == "bottom" {
					index = 2
				}
				n, e := s.choose("Icon alignment", "Only standard rows support automatic Apply.", names, index)
				if e == errBack {
					continue
				}
				if e != nil {
					return e
				}
				p.Alignment = []string{"preserve", "standard", "bottom"}[n]
			case 4:
				s.title = "Manual widget space"
				s.history = []string{"Space reservations are a manual preference. They block automatic Apply because cfgutil cannot represent empty slots or widget geometry.", "Current request: " + p.WidgetSpace}
				text, e := s.ask("Describe space to reserve (empty clears): ")
				if e == errBack {
					continue
				}
				if e != nil {
					return e
				}
				p.WidgetSpace = text
			case 5:
				if p.AcceptReflow {
					p.AcceptReflow = false
				} else {
					s.title = "Widget and spacing limitation"
					s.history = []string{"Bulk placement can remove EXISTING widgets and reflow empty spaces.", "Standard rows contain no reserved slots or widget geometry. You must add/configure widgets manually afterward. Saved exports cannot guarantee restoration."}
					text, e := s.ask("Type yes to accept; Enter keeps No: ")
					if e == errBack {
						continue
					}
					if e != nil {
						return e
					}
					p.AcceptReflow = yes(text)
				}
			}
		}
		if !reflect.DeepEqual(previous, p) {
			s.app.Preferences = p
			s.app.invalidate()
			if _, err := s.app.save("preferences", p); err != nil {
				return err
			}
		}
	}
}
func (s *screen) choose(title, subtitle string, choices []string, selected int) (int, error) {
	for {
		rows := s.rows()
		start := max(0, selected-rows+1)
		body := []screenLine{}
		for i := start; i < min(len(choices), start+rows); i++ {
			prefix := "  "
			tone := ""
			if i == selected {
				prefix = "› "
				tone = "selected"
			}
			body = append(body, screenLine{prefix + choices[i], tone})
		}
		fits := s.paint(title, subtitle, body, "↑↓ / j k move   Enter choose   Esc back")
		k, err := s.key()
		if err != nil {
			return 0, err
		}
		if isBack(k) {
			return 0, errBack
		}
		if !fits {
			continue
		}
		if down(k) {
			selected = (selected + 1) % len(choices)
		}
		if up(k) {
			selected = (selected + len(choices) - 1) % len(choices)
		}
		if k.Name == "enter" {
			return selected, nil
		}
	}
}
func position(ids []string, id string) int {
	for i, key := range ids {
		if id == key {
			return i
		}
	}
	return -1
}
func toggle(ids []string, id string, maxCount int) []string {
	result := append([]string{}, ids...)
	i := position(result, id)
	if i >= 0 {
		return append(result[:i], result[i+1:]...)
	}
	if maxCount > 0 && len(result) >= maxCount {
		return result
	}
	return append(result, id)
}

func (s *screen) pickApps(state core.State, current []string, dock bool) ([]string, bool, error) {
	chosen := append([]string{}, current...)
	if dock && current == nil {
		for _, item := range state.Items {
			if item.SourcePage == 0 && item.Kind == "icon" {
				chosen = append(chosen, item.ID)
			}
		}
	}
	title := "Must-keep apps"
	if dock {
		title = "Choose your Dock"
	}
	query := ""
	searching := false
	selected := 0
	notice := "Space toggles; Enter saves your selection."
	for {
		items := []core.Item{}
		for _, item := range state.Items {
			if dock && item.Kind != "icon" {
				continue
			}
			haystack := strings.ToLower(item.Label + " " + item.SourceID + " " + strings.Join(item.SourceFolders, " "))
			if strings.Contains(haystack, strings.ToLower(query)) {
				items = append(items, item)
			}
		}
		selected = min(selected, max(0, len(items)-1))
		w, _ := s.size()
		count := fmt.Sprintf("%d selected", len(chosen))
		if dock {
			count = fmt.Sprintf("%d / 4 selected · selection order becomes Dock order", len(chosen))
		}
		body := []screenLine{dim("Search: " + query)}
		if searching {
			body[0] = accent("Search: " + query + "▏")
		}
		listRows := max(1, s.rows()-5)
		start := max(0, selected-listRows+1)
		if len(items) == 0 {
			body = append(body, dim("No matching apps. Change or clear the search."))
		}
		for i := start; i < min(len(items), start+listRows); i++ {
			item := items[i]
			mark := "[ ]"
			n := position(chosen, item.ID)
			if n >= 0 {
				mark = "[✓]"
				if dock {
					mark = fmt.Sprintf("[%d]", n+1)
				}
			}
			prefix := "  "
			tone := ""
			if i == selected {
				prefix = "› "
				tone = "selected"
			}
			body = append(body, screenLine{prefix + mark + " " + clip(item.Label, w-12), tone})
		}
		for len(body) < listRows+1 {
			body = append(body, line(""))
		}
		if len(items) > 0 {
			detail := items[selected]
			body = append(body, dim(clip(detail.Label, w-3)), dim(source(detail)))
		} else {
			body = append(body, line(""), line(""))
		}
		body = append(body, dim(notice))
		footer := "↑↓ move  Space toggle  / search  Enter save  Esc back"
		if dock {
			footer = "↑↓ Space  [ ] order  p keep  Enter save  Esc back"
		}
		if searching {
			footer = "Type to search  Backspace edit  Enter / Esc finish"
		}
		fits := s.paint(title, count, body, footer)
		k, err := s.key()
		if err != nil {
			return nil, false, err
		}
		if !fits {
			if isBack(k) {
				return nil, false, nil
			}
			continue
		}
		if searching {
			switch k.Name {
			case "esc", "enter":
				searching = false
			case "backspace":
				r := []rune(query)
				if len(r) > 0 {
					query = string(r[:len(r)-1])
					selected = 0
				}
			case "ctrl-u":
				query = ""
				selected = 0
			case "rune":
				query += string(k.Rune)
				selected = 0
			case "space":
				query += " "
				selected = 0
			}
			continue
		}
		if isBack(k) {
			return nil, false, nil
		}
		if k.Name == "enter" {
			return chosen, true, nil
		}
		if isRune(k, '/') {
			searching = true
			continue
		}
		if dock && isRune(k, 'p') {
			return nil, true, nil
		}
		if len(items) == 0 {
			continue
		}
		if down(k) {
			selected = (selected + 1) % len(items)
		}
		if up(k) {
			selected = (selected + len(items) - 1) % len(items)
		}
		if k.Name == "space" {
			limit := 0
			if dock {
				limit = 4
			}
			if dock && len(chosen) >= 4 && position(chosen, items[selected].ID) < 0 {
				notice = "Dock is full. Deselect an app before adding another."
			} else {
				chosen = toggle(chosen, items[selected].ID, limit)
				notice = "Selection updated. Enter saves; Escape discards."
			}
		}
		if dock && (isRune(k, '[') || isRune(k, ']')) {
			n := position(chosen, items[selected].ID)
			next := n
			if isRune(k, '[') {
				next--
			} else {
				next++
			}
			if n >= 0 && next >= 0 && next < len(chosen) {
				chosen[n], chosen[next] = chosen[next], chosen[n]
				notice = "Dock order updated."
			}
		}
	}
}
