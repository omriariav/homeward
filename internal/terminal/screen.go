package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/omriariav/homeward/internal/organizer"
)

var errBack = errors.New("back")
var errQuit = errors.New("quit")

type screenInput interface {
	Size() (int, int)
	ReadKey(context.Context) (Key, error)
	FlushInput() error
}
type screenLine struct {
	text string
	tone string
}

func line(text string) screenLine   { return screenLine{text: text} }
func dim(text string) screenLine    { return screenLine{text: text, tone: "dim"} }
func accent(text string) screenLine { return screenLine{text: text, tone: "accent"} }

type screen struct {
	app      *App
	input    screenInput
	output   io.Writer
	color    bool
	ctx      context.Context
	title    string
	history  []string
	previous string
	notice   string
	writeErr error
}

// RunScreen owns alternate-screen/cursor cleanup. The caller also closes the raw
// terminal handle, whose Close restores the original termios even after errors.
func RunScreen(ctx context.Context, app *App, input *ScreenTerminal, output io.Writer, color bool) error {
	return runScreen(ctx, app, input, output, color)
}
func runScreen(ctx context.Context, app *App, input screenInput, output io.Writer, color bool) (err error) {
	s := &screen{app: app, input: input, output: output, color: color, ctx: ctx, notice: "Start with your context, then shape a proposal."}
	oldUI := app.UI
	app.UI = &Terminal{Out: output, Read: s.ask, PrintScreen: s.print, ShowScreen: s.show, WorkScreen: s.work}
	defer func() { app.UI = oldUI; fmt.Fprint(output, "\x1b[0m\x1b[?25h\x1b[?1049l") }()
	if _, err = fmt.Fprint(output, "\x1b[?1049h\x1b[?25l"); err != nil {
		return err
	}
	err = s.dashboard()
	if errors.Is(err, errQuit) || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
func (s *screen) print(text string) {
	s.history = append(s.history, strings.Split(text, "\n")...)
	if len(s.history) > 300 {
		s.history = s.history[len(s.history)-300:]
	}
}
func (s *screen) key() (Key, error) {
	if s.writeErr != nil {
		return Key{}, s.writeErr
	}
	k, err := s.input.ReadKey(s.ctx)
	if err != nil {
		return k, err
	}
	if k.Name == "ctrl-c" || k.Name == "eof" {
		return k, errQuit
	}
	return k, nil
}
func isRune(k Key, r rune) bool { return k.Name == "rune" && k.Rune == r }
func isBack(k Key) bool         { return k.Name == "esc" || isRune(k, 'q') }
func down(k Key) bool           { return k.Name == "down" || isRune(k, 'j') }
func up(k Key) bool             { return k.Name == "up" || isRune(k, 'k') }
func (s *screen) size() (int, int) {
	w, h := s.input.Size()
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}
	return w, h
}
func (s *screen) rows() int { _, h := s.size(); return max(1, h-9) }
func visibleWidth(text string) int {
	n := 0
	for _, r := range text {
		n += runeColumns(r)
	}
	return n
}
func runeColumns(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a || (r >= 0x2e80 && r <= 0xa4cf) || (r >= 0xac00 && r <= 0xd7a3) || (r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe10 && r <= 0xfe6f) || (r >= 0xff01 && r <= 0xff60) || (r >= 0xffe0 && r <= 0xffe6) || (r >= 0x1f300 && r <= 0x1faff) || (r >= 0x20000 && r <= 0x3ffff)) {
		return 2
	}
	return 1
}
func clip(text string, width int) string {
	text = Safe(text)
	if width <= 0 {
		return ""
	}
	if visibleWidth(text) <= width {
		return text
	}
	var b strings.Builder
	n := 0
	for _, r := range text {
		size := runeColumns(r)
		if n+size > width-1 {
			break
		}
		b.WriteRune(r)
		n += size
	}
	return b.String() + "…"
}
func pad(text string, width int) string {
	text = clip(text, width)
	return text + strings.Repeat(" ", max(0, width-visibleWidth(text)))
}
func wrap(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	out := []string{}
	for _, paragraph := range strings.Split(text, "\n") {
		paragraph = Safe(paragraph)
		if paragraph == "" {
			out = append(out, "")
			continue
		}
		var b strings.Builder
		n := 0
		for _, r := range paragraph {
			size := runeColumns(r)
			if n+size > width && n > 0 {
				out = append(out, b.String())
				b.Reset()
				n = 0
			}
			b.WriteRune(r)
			n += size
		}
		out = append(out, b.String())
	}
	return out
}
func (s *screen) paint(title, subtitle string, body []screenLine, footer string) bool {
	w, h := s.size()
	fits := w >= 56 && h >= 20
	mode := "LOCAL"
	if s.app.Options.Demo {
		mode = "DEMO"
	} else if s.app.Options.Device != "" {
		mode = "PHONE"
	}
	rows := []screenLine{accent("HOMEWARD  /  " + mode), dim("A home screen with room for what matters."), line(""), accent(title), dim(subtitle), line("")}
	if !fits {
		rows = []screenLine{accent("HOMEWARD"), line(""), line("A little more room, please."), dim("Resize to at least 56 × 20."), dim("Or relaunch with --plain.")}
		footer = "q quit · Esc back"
	}
	capacity := max(0, h-9)
	if len(body) > capacity {
		body = body[:capacity]
	}
	if fits {
		rows = append(rows, body...)
	}
	for len(rows) < max(0, h-2) {
		rows = append(rows, line(""))
	}
	if len(rows) > max(0, h-2) {
		rows = rows[:max(0, h-2)]
	}
	rows = append(rows, dim(strings.Repeat("─", max(0, min(w-1, 100)))), dim(footer))
	if len(rows) > h {
		rows = rows[len(rows)-h:]
	}
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, row := range rows {
		b.WriteString("\x1b[2K")
		text := clip(row.text, max(1, w-1))
		if s.color {
			switch row.tone {
			case "accent":
				b.WriteString("\x1b[36m")
			case "dim":
				b.WriteString("\x1b[2m")
			case "selected":
				b.WriteString("\x1b[1;36m")
			}
		}
		b.WriteString(text)
		if s.color {
			b.WriteString("\x1b[0m")
		}
		if i < len(rows)-1 {
			b.WriteString("\r\n")
		}
	}
	frame := b.String()
	if frame != s.previous {
		_, s.writeErr = fmt.Fprint(s.output, frame)
		s.previous = frame
	}
	return fits
}
func (s *screen) applyProblems() []string {
	if s.app.Review == nil {
		return []string{"Create a proposal first."}
	}
	problems := organizer.Blockers(*s.app.Review, s.app.Device.CleanupAvailable())
	if s.app.Reviewed != s.app.Review.Digest() {
		problems = append([]string{"Read every proposal section and finish the review."}, problems...)
	}
	return problems
}
func (s *screen) dashboard() error {
	selected := 0
	for {
		apps := "No layout loaded"
		if state, err := s.app.state(); err == nil {
			widgets := 0
			for _, item := range state.Items {
				if item.Kind == "widget" {
					widgets++
				}
			}
			apps = fmt.Sprintf("%d apps", len(state.Items)-widgets)
			if widgets > 0 {
				apps += fmt.Sprintf(" · %d widgets", widgets)
			}
		}
		ai := "AI asks each time"
		if s.app.RefusedAI {
			ai = "Local only"
		}
		contextText := s.app.Request
		if contextText == "" {
			contextText = "Tell us what deserves the fastest access."
		}
		proposal := "No proposal yet"
		if s.app.Review != nil {
			p := s.app.Review.Data().Artifact.Plan
			count := len(p.Dock)
			for _, page := range p.Pages {
				count += len(page.Items)
			}
			proposal = fmt.Sprintf("%s · %d selected · %d off Home Screen", plural(len(p.Pages), "page", "pages"), count, len(p.Hidden))
		}
		labels := []string{"Read / import layout", "Explore apps", "Your context", "Layout preferences", "Interview", "Create proposal", "Review proposal", "Apply reviewed proposal"}
		if s.app.Options.Demo {
			labels[0] = "Reset demo layout"
		}
		problems := s.applyProblems()
		if len(problems) > 0 {
			labels[7] = "Apply · unavailable"
		}
		body := []screenLine{line("Context  " + contextText), line("Proposal " + proposal), line("")}
		for i, label := range labels {
			marker := "  "
			tone := ""
			if selected == i {
				marker = "› "
				tone = "selected"
			}
			body = append(body, screenLine{fmt.Sprintf("%s%d  %s", marker, i+1, label), tone})
		}
		body = append(body, line(""), dim(s.notice))
		if selected == 7 && len(problems) > 0 {
			body[len(body)-1] = dim(problems[0] + " Enter for details.")
		}
		target := "Saved layout"
		if s.app.Options.Demo {
			target = "Demo iPhone"
		} else if s.app.Options.Device != "" {
			target = "iPhone configured"
			if s.app.Snapshot != nil {
				target = "iPhone snapshot"
			}
		}
		fits := s.paint("Make it yours", target+" · "+apps+" · "+ai, body, "↑↓ / j k move  Enter open  1–8  ? details  q quit")
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
		if isRune(k, '?') {
			_, err = s.viewer("Session details", "Mode: "+target+"\n\nEvidence and saved answers:\n"+s.app.Directory+"\n\nNo phone or AI requests happen at startup. Device status is a saved snapshot, not a live connection indicator.")
			if err != nil {
				return err
			}
			continue
		}
		activate := k.Name == "enter"
		if k.Name == "rune" && k.Rune >= '1' && k.Rune <= '8' {
			selected = int(k.Rune - '1')
			activate = true
		}
		if !activate {
			continue
		}
		s.title = labels[selected]
		s.history = nil
		s.previous = ""
		switch selected {
		case 0:
			err = s.app.Capture(s.ctx)
		case 1:
			err = s.app.Inventory()
		case 2:
			err = s.app.Context()
		case 3:
			err = s.preferences()
		case 4:
			err = s.app.Questionnaire(s.ctx)
		case 5:
			err = s.app.Generate(s.ctx)
		case 6:
			err = s.app.View()
		case 7:
			if len(problems) > 0 {
				_, err = s.viewer("Apply unavailable", strings.Join(problems, "\n\n"))
			} else {
				err = s.app.Apply(s.ctx)
			}
		}
		if errors.Is(err, errQuit) || errors.Is(err, io.EOF) {
			return nil
		}
		if errors.Is(err, context.Canceled) && s.ctx.Err() != nil {
			return err
		}
		if errors.Is(err, errBack) {
			s.notice = "Back to your Home Screen plan."
		} else if err != nil {
			s.notice = "Stopped: " + err.Error()
			if _, e := s.viewer("Action stopped", err.Error()); e != nil && !errors.Is(e, errBack) {
				return e
			}
		} else {
			s.notice = "Ready. Your changes and evidence stay in this session."
			if len(s.history) > 0 {
				s.notice = s.history[len(s.history)-1]
			}
		}
	}
}
func (s *screen) show(text string) (bool, error) {
	if strings.HasPrefix(text, "CURRENT SAVED INVENTORY") {
		return s.inventory()
	}
	if strings.HasPrefix(text, "PROPOSED HOME SCREEN") && s.app.Review != nil {
		return s.proposal()
	}
	return s.viewer(s.title, text)
}
func (s *screen) viewer(title, text string) (bool, error) {
	offset := 0
	for {
		w, _ := s.size()
		raw := wrap(text, w-4)
		rows := s.rows()
		offset = min(offset, max(0, len(raw)-rows))
		body := []screenLine{}
		for _, value := range raw[offset:min(len(raw), offset+rows)] {
			body = append(body, line(value))
		}
		fits := s.paint(title, fmt.Sprintf("%d–%d of %d lines", offset+1, min(len(raw), offset+rows), len(raw)), body, "↑↓ scroll   PgUp/PgDn page   Enter / Esc back")
		k, err := s.key()
		if err != nil {
			return false, err
		}
		if isBack(k) || k.Name == "enter" {
			return true, nil
		}
		if !fits {
			continue
		}
		if down(k) {
			offset = min(offset+1, max(0, len(raw)-rows))
		}
		if up(k) {
			offset = max(0, offset-1)
		}
		if k.Name == "pagedown" || k.Name == "space" {
			offset = min(offset+rows, max(0, len(raw)-rows))
		}
		if k.Name == "pageup" {
			offset = max(0, offset-rows)
		}
		if k.Name == "home" {
			offset = 0
		}
		if k.Name == "end" {
			offset = max(0, len(raw)-rows)
		}
	}
}

// Typed forms never select Yes by focus. Clearing both parser and kernel queues
// prevents navigation/pasted keys from carrying into a new consent/Apply prompt.
func (s *screen) ask(prompt string) (string, error) {
	if err := s.input.FlushInput(); err != nil {
		return "", err
	}
	if strings.Contains(prompt, "Send this information") || strings.HasPrefix(prompt, "Type APPLY ") || strings.Contains(prompt, "Type yes to accept") {
		complete, err := s.readDisclosure(strings.Join(s.history, "\n\n"))
		if err != nil {
			return "", err
		}
		if !complete {
			return "", errBack
		}
		if err := s.input.FlushInput(); err != nil {
			return "", err
		}
	}
	value := []rune{}
	historyOffset := -1
	for {
		w, _ := s.size()
		rows := s.rows()
		all := []string{}
		for _, text := range s.history {
			all = append(all, wrap(text, w-4)...)
		}
		historyRows := max(1, rows-4)
		start := max(0, len(all)-historyRows)
		if historyOffset >= 0 {
			start = min(historyOffset, start)
		}
		body := []screenLine{}
		for _, text := range all[start:min(len(all), start+historyRows)] {
			body = append(body, line(text))
		}
		for len(body) < historyRows {
			body = append(body, line(""))
		}
		body = append(body, accent(prompt))
		inputText := string(value)
		limit := max(1, w-8)
		for visibleWidth(inputText) > limit {
			_, n := firstRune(inputText)
			inputText = inputText[n:]
		}
		body = append(body, line("› "+inputText+"▏"), dim("Typed input · Enter submits · Escape cancels"))
		fits := s.paint(s.title, "Your answers are saved locally. Nothing is assumed.", body, "Enter submit   Esc back   Ctrl-U clear   ↑↓ disclosure")
		k, err := s.key()
		if err != nil {
			return "", err
		}
		if k.Name == "esc" {
			return "", errBack
		}
		if !fits {
			if isRune(k, 'q') {
				return "", errQuit
			}
			continue
		}
		switch k.Name {
		case "enter":
			return strings.TrimSpace(string(value)), nil
		case "backspace":
			if len(value) > 0 {
				value = value[:len(value)-1]
			}
		case "ctrl-u":
			value = nil
		case "rune":
			if !unicode.IsControl(k.Rune) {
				value = append(value, k.Rune)
			}
		case "space":
			value = append(value, ' ')
		case "up", "pageup":
			if historyOffset < 0 {
				historyOffset = start
			}
			historyOffset = max(0, historyOffset-historyRows)
		case "down", "pagedown":
			if historyOffset >= 0 {
				historyOffset = min(max(0, len(all)-historyRows), historyOffset+historyRows)
			}
		}
	}
}
func firstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

func (s *screen) work(ctx context.Context, title string, fn func(context.Context) error) error {
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- fn(work) }()
	start := time.Now()
	cancelled := false
	frames := []string{"◐", "◓", "◑", "◒"}
	for {
		select {
		case err := <-done:
			if s.writeErr != nil {
				return s.writeErr
			}
			s.previous = ""
			if flushErr := s.input.FlushInput(); err == nil {
				err = flushErr
			}
			return err
		default:
		}
		elapsed := time.Since(start)
		text := title
		if cancelled {
			text = "Cancelling; waiting for the operation to stop…"
		}
		s.paint("Working", text, []screenLine{line(""), accent(fmt.Sprintf("%s  %s", frames[int(elapsed/(200*time.Millisecond))%len(frames)], title)), dim(fmt.Sprintf("Elapsed %s · no progress estimate", elapsed.Truncate(time.Second))), line(""), dim("Device operations may leave a partial state when cancelled."), dim("Any failure is recorded with evidence; there is no blind retry.")}, "Esc / Ctrl-C cancel · wait for a safe return")
		// ReadKey has bounded polling; only this goroutine touches terminal input.
		key, err := s.input.ReadKey(ctx)
		if err != nil || s.writeErr != nil || key.Name == "esc" || key.Name == "ctrl-c" || key.Name == "eof" || isRune(key, 'q') {
			cancel()
			cancelled = true
		}
		if cancelled {
			select {
			case err := <-done:
				return err
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}
