// Package device contains injectable device transports. Constructing a transport
// does not read a phone or start a subprocess. The organizer owns review/approval.
package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// Snapshot is a cfgutil icon-order capture, not a backup of widgets/free positions.
// AppIDs is a complete catalog of known app identifiers, StageIDs its launchable
// subset plus known apps observed in Layout. Neither treats unknown URLs as apps.
type Snapshot struct {
	Identity map[string]string `json:"identity"`
	Layout   []any             `json:"layout"`
	AppIDs   []string          `json:"app_ids,omitempty"`
	StageIDs []string          `json:"stage_ids,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Warnings []string          `json:"warnings,omitempty"`
}

type Device interface {
	Capture(context.Context) (Snapshot, error)
	Preflight(context.Context) error
	Place(context.Context, []any, string) error
	Cleanup(context.Context, string, int) error
	ReadLayout(context.Context) ([]any, error)
	CleanupAvailable() bool
	Simulated() bool
}

// Runner receives argv, never a shell command. Its context has a deadline.
type Runner func(context.Context, []string) (stdout, stderr []byte, err error)

// Live implements Apple-tool operations. Run is replaceable for synthetic tests.
type Live struct {
	Root, UDID, ECID, EvidenceDir, Cfgutil string
	Run                                    Runner
	mu                                     sync.Mutex
	sequence                               uint64
}

func NewLive(root, udid, ecid, evidenceDir, cfgutil string) *Live {
	if cfgutil == "" {
		cfgutil = "/Applications/Apple Configurator.app/Contents/MacOS/cfgutil"
	}
	return &Live{Root: root, UDID: udid, ECID: strings.ToLower(ecid), EvidenceDir: evidenceDir, Cfgutil: cfgutil}
}

func (l *Live) Simulated() bool { return false }

func (l *Live) runnerFile() (string, error) {
	paths, err := filepath.Glob(filepath.Join(l.Root, ".build", "device", "Build", "Products", "HomeScreenPOC_*.xctestrun"))
	if err != nil {
		return "", err
	}
	if len(paths) != 1 {
		return "", fmt.Errorf("build the signed physical XCTest runner first; expected one HomeScreenPOC_*.xctestrun, found %d; cfgutil-only cleanup is unsupported", len(paths))
	}
	return paths[0], nil
}

func (l *Live) CleanupAvailable() bool { _, err := l.runnerFile(); return err == nil }

// privateJSON creates evidence exclusively; a prior artifact is never overwritten.
func privateJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (l *Live) command(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("empty subprocess argv")
	}
	// Evidence must be writable before any command can run, especially mutations.
	if l.EvidenceDir == "" {
		return nil, errors.New("device commands require a private evidence directory")
	}
	if err := os.MkdirAll(l.EvidenceDir, 0700); err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.sequence++
	number := l.sequence
	l.mu.Unlock()
	stem := fmt.Sprintf("command-%d-%04d", time.Now().UnixNano(), number)
	path := filepath.Join(l.EvidenceDir, stem+".json")
	record := map[string]any{"args": args, "started_utc": time.Now().UTC().Format(time.RFC3339Nano)}
	// Record intent before execution. A crash can leave an intent with no outcome.
	if err := privateJSON(filepath.Join(l.EvidenceDir, stem+"-intent.json"), record); err != nil {
		return nil, err
	}
	run := l.Run
	if run == nil {
		run = RunCommand
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, stderr, err := run(deadline, append([]string(nil), args...))
	record["stdout"], record["stderr"] = string(stdout), string(stderr)
	record["completed_utc"] = time.Now().UTC().Format(time.RFC3339Nano)
	if err != nil {
		record["error"] = err.Error()
	}
	if ctxErr := deadline.Err(); ctxErr != nil {
		record["context_error"] = ctxErr.Error()
		if err == nil {
			err = ctxErr
		}
	}
	if logErr := privateJSON(path, record); logErr != nil {
		return stdout, fmt.Errorf("command outcome logging failed (command may have run): %w", logErr)
	}
	if err != nil {
		return stdout, fmt.Errorf("%s failed: %w; inspect %s", filepath.Base(args[0]), err, path)
	}
	return stdout, nil
}

func (l *Live) cfg(ctx context.Context, formatted bool, command ...string) ([]byte, error) {
	args := []string{l.Cfgutil}
	if formatted {
		args = append(args, "--format", "JSON")
	}
	args = append(args, "--ecid", l.ECID, "--timeout", "15")
	args = append(args, command...)
	return l.command(ctx, time.Minute, args...)
}

func (l *Live) property(ctx context.Context, name string) (json.RawMessage, error) {
	raw, err := l.cfg(ctx, true, "get", name)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Output map[string]json.RawMessage `json:"Output"`
		Errors json.RawMessage            `json:"Errors"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("invalid cfgutil response: %w", err)
	}
	if hasErrors(envelope.Errors) || hasErrors(envelope.Output["Errors"]) {
		return nil, errors.New("cfgutil reported errors; refusing an incomplete identity response")
	}
	var selected json.RawMessage
	matches := 0
	for key, value := range envelope.Output {
		if strings.EqualFold(key, l.ECID) {
			selected = value
			matches++
		}
	}
	if matches != 1 {
		return nil, fmt.Errorf("cfgutil did not return exactly one record for ECID %s", l.ECID)
	}
	var properties map[string]json.RawMessage
	if err = json.Unmarshal(selected, &properties); err != nil {
		return nil, err
	}
	value, ok := properties[name]
	if !ok {
		return nil, fmt.Errorf("cfgutil response is missing %s", name)
	}
	return value, nil
}

// Empty per-device Errors dictionaries are common and do not indicate failure.
func hasErrors(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	var nonempty func(any) bool
	nonempty = func(v any) bool {
		switch x := v.(type) {
		case nil:
			return false
		case map[string]any:
			for _, child := range x {
				if nonempty(child) {
					return true
				}
			}
			return false
		case []any:
			return len(x) != 0
		case string:
			return x != ""
		case bool:
			return x
		default:
			return true
		}
	}
	return nonempty(value)
}

func parseLayout(raw []byte) ([]any, error) {
	var layout []any
	if err := json.Unmarshal(raw, &layout); err != nil {
		return nil, fmt.Errorf("invalid cfgutil layout JSON: %w", err)
	}
	if len(layout) < 2 {
		return nil, errors.New("layout needs a Dock and at least one Home Screen page")
	}
	for _, page := range layout {
		if _, ok := page.([]any); !ok {
			return nil, errors.New("layout pages must be arrays")
		}
	}
	return layout, nil
}

func (l *Live) ReadLayout(ctx context.Context) ([]any, error) {
	raw, err := l.cfg(ctx, false, "get-icon-layout")
	if err != nil {
		return nil, err
	}
	return parseLayout(raw)
}

// observedIDs understands cfgutil folder pages. It never includes folder names.
func observedIDs(layout []any) []string {
	var ids []string
	var walk func(any)
	walk = func(node any) {
		switch x := node.(type) {
		case string:
			if x != "" {
				ids = append(ids, x)
			}
		case []any:
			if len(x) == 0 {
				return
			}
			// node is a folder: first value is its name, children may be pages.
			for _, child := range x[1:] {
				if page, ok := child.([]any); ok {
					for _, nested := range page {
						walk(nested)
					}
				} else {
					walk(child)
				}
			}
		}
	}
	for _, page := range layout {
		if slots, ok := page.([]any); ok {
			for _, node := range slots {
				walk(node)
			}
		}
	}
	return ids
}

func (l *Live) deviceJSON(ctx context.Context, kind string, extra ...string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "homeward-device-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, kind+".json")
	args := []string{"xcrun", "devicectl", "device", "info", kind, "--device", l.UDID}
	args = append(args, extra...)
	args = append(args, "--json-output", path, "--timeout", "30")
	if _, err = l.command(ctx, time.Minute, args...); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("devicectl did not produce its JSON result: %w", err)
	}
	// Keep file-based result alongside command logs before temporary cleanup.
	var decoded any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	if err = privateJSON(filepath.Join(l.EvidenceDir, fmt.Sprintf("devicectl-%s-%d.json", kind, time.Now().UnixNano())), decoded); err != nil {
		return nil, err
	}
	return raw, nil
}

func (l *Live) Capture(ctx context.Context) (Snapshot, error) {
	var snapshot Snapshot
	raw, err := l.property(ctx, "UDID")
	if err != nil {
		return snapshot, err
	}
	var udid string
	if err = json.Unmarshal(raw, &udid); err != nil || udid == "" || udid != l.UDID {
		return snapshot, errors.New("ECID and UDID do not identify the same phone; no placement attempted")
	}
	layout, err := l.ReadLayout(ctx)
	if err != nil {
		return snapshot, err
	}
	raw, err = l.deviceJSON(ctx, "apps", "--include-all-apps")
	if err != nil {
		return snapshot, err
	}
	var catalog struct {
		Info struct {
			Outcome string `json:"outcome"`
		} `json:"info"`
		Result struct {
			Apps []struct {
				BundleIdentifier string `json:"bundleIdentifier"`
				Name             string `json:"name"`
				Hidden           bool   `json:"hidden"`
				InternalApp      bool   `json:"internalApp"`
				AppClip          bool   `json:"appClip"`
			} `json:"apps"`
		} `json:"result"`
	}
	if err = json.Unmarshal(raw, &catalog); err != nil {
		return snapshot, err
	}
	if catalog.Info.Outcome != "success" || len(catalog.Result.Apps) == 0 {
		return snapshot, errors.New("devicectl did not return a complete app inventory; import a saved layout for read-only review")
	}
	known, stage, labels := map[string]bool{}, map[string]bool{}, map[string]string{}
	for _, app := range catalog.Result.Apps {
		if app.BundleIdentifier == "" {
			return snapshot, errors.New("devicectl app inventory contains an empty bundle identifier")
		}
		known[app.BundleIdentifier] = true
		if app.Name != "" {
			labels[app.BundleIdentifier] = app.Name
		}
		if !app.Hidden && !app.InternalApp && !app.AppClip {
			stage[app.BundleIdentifier] = true
		}
	}
	var unresolved []string
	for _, id := range observedIDs(layout) {
		if known[id] {
			stage[id] = true
		} else {
			unresolved = append(unresolved, id)
		}
	}
	snapshot = Snapshot{Identity: map[string]string{"udid": l.UDID, "ecid": l.ECID}, Layout: layout,
		AppIDs: sortedKeys(known), StageIDs: sortedKeys(stage), Labels: labels}
	if len(unresolved) > 0 {
		snapshot.Warnings = append(snapshot.Warnings, "Some layout identifiers are absent from the complete app catalog. Keep shortcuts/non-app items protected; use a manual workflow for them.")
	}
	// cfgutil installedApps can exit 1 with partial data and omits built-ins. It is
	// deliberately not needed: the complete devicectl catalog is the authority.
	return snapshot, nil
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func (l *Live) loadRunner(ctx context.Context) (map[string]any, string, error) {
	source, err := l.runnerFile()
	if err != nil {
		return nil, "", err
	}
	raw, err := l.command(ctx, 15*time.Second, "/usr/bin/plutil", "-convert", "json", "-o", "-", source)
	if err != nil {
		return nil, "", err
	}
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		return nil, "", fmt.Errorf("invalid xctestrun plist: %w", err)
	}
	return data, source, nil
}

// configureRunner supports legacy and TestConfigurations xctestrun dictionaries.
func configureRunner(data map[string]any, command map[string]any) error {
	raw, err := json.Marshal(command)
	if err != nil {
		return err
	}
	count := 0
	var invalid bool
	var walk func(any)
	walk = func(node any) {
		switch x := node.(type) {
		case map[string]any:
			if _, ok := x["TestBundlePath"]; ok {
				count++
				env, ok := x["EnvironmentVariables"].(map[string]any)
				if !ok {
					if x["EnvironmentVariables"] != nil {
						invalid = true
						return
					}
					env = map[string]any{}
					x["EnvironmentVariables"] = env
				}
				env["HSC_COMMAND"] = string(raw)
				return
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(data)
	if count != 1 || invalid {
		return fmt.Errorf("expected one valid XCTest target, found %d (invalid environment: %t)", count, invalid)
	}
	return nil
}

func (l *Live) Preflight(ctx context.Context) error {
	// Validate plist conversion/target before cfgutil placement can be attempted.
	data, _, err := l.loadRunner(ctx)
	if err != nil {
		return err
	}
	if err = configureRunner(data, map[string]any{"action": "hide-other-pages", "keepPages": 1}); err != nil {
		return err
	}
	raw, err := l.deviceJSON(ctx, "lockState")
	if err != nil {
		return err
	}
	var response struct {
		Info struct {
			Outcome string `json:"outcome"`
		} `json:"info"`
		Result struct {
			PasscodeRequired *bool `json:"passcodeRequired"`
		} `json:"result"`
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		return err
	}
	if response.Info.Outcome != "success" || response.Result.PasscodeRequired == nil || *response.Result.PasscodeRequired {
		return errors.New("could not verify an unlocked phone; unlock it and keep it awake during Apply")
	}
	return nil
}

func (l *Live) Place(ctx context.Context, layout []any, path string) error {
	// Bind the file passed to cfgutil to the staged in-memory bytes, not its name.
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	staged, err := parseLayout(raw)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(layout)
	if err != nil {
		return err
	}
	normalized, err := parseLayout(encoded)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(staged, normalized) {
		return errors.New("staging file changed; refusing cfgutil placement")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	_, err = l.cfg(ctx, false, "set-icon-layout", "--force", absolute)
	return err
}

func (l *Live) Cleanup(ctx context.Context, action string, keepPages int) error {
	if action != "hide-other-pages" && action != "remove-hidden-pages" {
		return errors.New("unsupported cleanup action")
	}
	if keepPages < 1 || keepPages > 15 {
		return errors.New("keep-pages must be between 1 and 15")
	}
	data, source, err := l.loadRunner(ctx)
	if err != nil {
		return err
	}
	if err = configureRunner(data, map[string]any{"action": action, "keepPages": keepPages}); err != nil {
		return err
	}
	// Relative __TESTROOT__ paths require this file alongside the built original.
	file, err := os.CreateTemp(filepath.Dir(source), "homeward-command-*.xctestrun")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	raw, err := json.Marshal(data)
	if err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if _, err = l.command(ctx, 15*time.Second, "/usr/bin/plutil", "-convert", "xml1", path); err != nil {
		return err
	}
	resultPath, err := filepath.Abs(filepath.Join(l.EvidenceDir, fmt.Sprintf("%s-%d.xcresult", action, time.Now().UnixNano())))
	if err != nil {
		return err
	}
	_, err = l.command(ctx, 10*time.Minute, "xcodebuild", "test-without-building", "-xctestrun", path,
		"-destination", "id="+l.UDID, "-resultBundlePath", resultPath,
		"-parallel-testing-enabled", "NO", "-maximum-concurrent-test-device-destinations", "1")
	return err
}

// Local is a synthetic state machine for the complete review/apply loop. It does
// not launch any process and never treats observed placeholders as real apps.
type Local struct {
	State Snapshot
	Calls []string
}

func cloneSnapshot(s Snapshot) Snapshot {
	raw, _ := json.Marshal(s)
	var result Snapshot
	_ = json.Unmarshal(raw, &result)
	return result
}

func NewLocal(snapshot Snapshot) *Local {
	snapshot = cloneSnapshot(snapshot)
	if snapshot.AppIDs == nil {
		ids := map[string]bool{}
		for _, id := range observedIDs(snapshot.Layout) {
			if id != "/" && !strings.Contains(id, "://") {
				ids[id] = true
			}
		}
		snapshot.AppIDs = sortedKeys(ids)
	}
	if snapshot.StageIDs == nil {
		snapshot.StageIDs = append([]string{}, snapshot.AppIDs...)
	}
	return &Local{State: snapshot}
}

func (d *Local) Simulated() bool                     { return true }
func (d *Local) CleanupAvailable() bool              { return true }
func (d *Local) Preflight(ctx context.Context) error { return ctx.Err() }
func (d *Local) Capture(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return cloneSnapshot(d.State), nil
}
func (d *Local) ReadLayout(ctx context.Context) ([]any, error) {
	state, err := d.Capture(ctx)
	return state.Layout, err
}
func (d *Local) Place(ctx context.Context, layout []any, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	copy := cloneSnapshot(Snapshot{Layout: layout})
	d.State.Layout = copy.Layout
	d.Calls = append(d.Calls, "place")
	return nil
}
func (d *Local) Cleanup(ctx context.Context, action string, keepPages int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if action != "hide-other-pages" && action != "remove-hidden-pages" {
		return errors.New("unsupported cleanup action")
	}
	if keepPages < 1 || keepPages >= len(d.State.Layout) {
		return errors.New("invalid local keep-page count")
	}
	d.Calls = append(d.Calls, action)
	if action == "remove-hidden-pages" {
		d.State.Layout = d.State.Layout[:keepPages+1]
	}
	return nil
}
