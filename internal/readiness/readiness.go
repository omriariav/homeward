// Package readiness reports measured prerequisites separately from unverified
// execution capabilities. Inspect never starts a process; Check only reads.
package readiness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/homeward/internal/device"
)

type Status string

const (
	Ready       Status = "Ready"
	NeedsSetup  Status = "Needs setup"
	NotChecked  Status = "Not checked"
	Unverified  Status = "Unverified"
	Unsupported Status = "Unsupported"
)

type Config struct{ Root, Cfgutil, UDID, ECID string }
type Row struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
}
type Report struct {
	Rows      []Row  `json:"rows"`
	Checked   bool   `json:"checked"`
	CheckedAt string `json:"checked_at,omitempty"`
}

func (r Report) Find(key string) Row {
	for _, row := range r.Rows {
		if row.Key == key {
			return row
		}
	}
	return Row{Key: key, Status: NotChecked}
}
func (r *Report) set(key string, status Status, detail string) {
	for i := range r.Rows {
		if r.Rows[i].Key == key {
			r.Rows[i].Status = status
			r.Rows[i].Detail = detail
			return
		}
	}
}

type Checker struct {
	Run      device.Runner
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
	Glob     func(string) ([]string, error)
}

func normalized(config Config) Config {
	if config.Cfgutil == "" {
		config.Cfgutil = "/Applications/Apple Configurator.app/Contents/MacOS/cfgutil"
	}
	return config
}
func Inspect(config Config) Report { return (Checker{}).Inspect(config) }
func (c Checker) Inspect(config Config) Report {
	config = normalized(config)
	stat := c.Stat
	if stat == nil {
		stat = os.Stat
	}
	look := c.LookPath
	if look == nil {
		look = exec.LookPath
	}
	glob := c.Glob
	if glob == nil {
		glob = filepath.Glob
	}
	report := Report{Rows: []Row{
		{"wizard", "Wizard guided workflow", Ready, "Available offline. Demo and imported layouts need no phone or external AI."},
		{"cfgutil", "Configurator tool", NeedsSetup, "Install Apple Configurator or choose its cfgutil executable."},
		{"apple_tools", "Apple developer tools", NeedsSetup, "Install/select Xcode with devicectl. This is separate from phone readiness."},
		{"identity", "Phone connection and identity", NotChecked, "Explicitly check the configured phone to verify its ECID and UDID pairing."},
		{"unlocked", "Phone unlocked", NotChecked, "Lock state has not been read. Keep the phone awake during Apply."},
		{"runner", "XCTest runner build", NeedsSetup, "Build a physical runner with your signing identity. No runner is built automatically."},
		{"xctest_runtime", "Developer Mode / trust / UI Automation", Unverified, "Not exercised. A runner file cannot prove signing, trust, Developer Mode or UI Automation works."},
		{"app_placement", "App placement", NotChecked, "Requires Configurator, a matched unlocked phone and supported plan. Readiness never authorizes Apply."},
		{"page_removal", "Page and icon removal", Unverified, "Requires a working signed XCTest runner. cfgutil-only cleanup is limited/manual and may repopulate omitted apps."},
		{"widget_movement", "Widget movement", Unsupported, "Automatic widget movement/reconstruction is unsupported. Arrange widgets manually."},
		{"bottom_alignment", "Bottom icon alignment", Unsupported, "Automatic bottom positioning is unsupported. Use the proposal as a manual guide."},
		{"reserved_space", "Reserved widget / empty space", Unsupported, "Empty-slot geometry cannot be represented. A requested reservation blocks automatic Apply."},
	}}
	if info, err := stat(config.Cfgutil); err == nil && !info.IsDir() && info.Mode().Perm()&0111 != 0 {
		report.set("cfgutil", Ready, "Configurator executable found. This does not prove phone connection or placement works.")
	}
	if _, err := look("xcrun"); err == nil {
		report.set("apple_tools", Unverified, "Apple tool launcher found; the selected Xcode/devicectl has not been checked.")
	}
	if paths, err := glob(filepath.Join(config.Root, ".build", "device", "Build", "Products", "HomeScreenPOC_*.xctestrun")); err == nil && len(paths) == 1 {
		report.set("runner", Unverified, "One physical runner configuration exists. Signing and launch have not been verified; rebuild after moving the checkout.")
	}
	if config.UDID == "" || config.ECID == "" {
		report.set("identity", NeedsSetup, "No complete live target is configured. Supply both --device UDID and --ecid ECID, or continue locally.")
		report.set("app_placement", NeedsSetup, "Configure a matching live phone target before app placement. Local planning remains available.")
	}
	if report.Find("cfgutil").Status == NeedsSetup {
		report.set("app_placement", NeedsSetup, "Configurator is missing. Local planning remains available; phone placement is not ready.")
	}
	if report.Find("runner").Status == NeedsSetup {
		report.set("page_removal", NeedsSetup, "Build the physical XCTest runner. cfgutil alone cannot reliably remove omitted apps; use manual cleanup until XCTest works.")
	}
	return report
}
func (c Checker) command(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run := c.Run
	if run == nil {
		run = device.RunCommand
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stdout, _, err := run(bounded, args)
	if bounded.Err() != nil {
		return nil, bounded.Err()
	}
	return stdout, err
}

// Check is an explicit read-only action. It never launches XCTest or sends a
// placement command; those capabilities remain Unverified even after good reads.
func (c Checker) Check(ctx context.Context, config Config) (Report, error) {
	config = normalized(config)
	report := c.Inspect(config)
	if config.UDID == "" || config.ECID == "" {
		return report, nil
	}
	report.Checked = true
	report.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if report.Find("cfgutil").Status != Ready {
		return report, errors.New("Configurator is unavailable; phone readiness was not checked")
	}
	if _, err := c.command(ctx, "xcrun", "--find", "devicectl"); err != nil {
		report.set("apple_tools", NeedsSetup, "The selected Xcode does not provide a usable devicectl. Complete Xcode setup; no phone read was attempted.")
		return report, fmt.Errorf("developer-tool check failed: %w", err)
	}
	report.set("apple_tools", Ready, "The selected Xcode provides devicectl. This says nothing about XCTest runtime readiness.")
	raw, err := c.command(ctx, config.Cfgutil, "--format", "JSON", "--ecid", config.ECID, "--timeout", "15", "get", "UDID")
	if err != nil {
		report.set("identity", Unverified, "Could not read the configured phone identity. Check USB connection, unlock and the trust prompt.")
		return report, fmt.Errorf("phone identity read failed: %w", err)
	}
	if err = matchingIdentity(raw, config); err != nil {
		report.set("identity", NeedsSetup, "The requested ECID/UDID pairing was not verified. Check both identifiers before continuing.")
		report.set("app_placement", NeedsSetup, "Verify the live phone's ECID/UDID pairing before app placement. Local planning remains available.")
		return report, err
	}
	report.set("identity", Ready, "Read access and the configured ECID/UDID pairing were verified. XCTest trust and launch remain unverified.")
	temporary, err := os.MkdirTemp("", "homeward-readiness-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(temporary)
	path := filepath.Join(temporary, "lock.json")
	if _, err = c.command(ctx, "xcrun", "devicectl", "device", "info", "lockState", "--device", config.UDID, "--json-output", path, "--timeout", "15"); err != nil {
		report.set("unlocked", Unverified, "Could not verify lock state. Unlock the phone and check its connection.")
		return report, fmt.Errorf("phone lock-state read failed: %w", err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		report.set("unlocked", Unverified, "The phone check returned no readable lock-state result; unlocked state was not established.")
		return report, fmt.Errorf("lock-state result unavailable: %w", err)
	}
	var lock struct {
		Info struct {
			Outcome string `json:"outcome"`
		} `json:"info"`
		Result struct {
			PasscodeRequired *bool `json:"passcodeRequired"`
		} `json:"result"`
	}
	if err = json.Unmarshal(raw, &lock); err != nil || lock.Info.Outcome != "success" || lock.Result.PasscodeRequired == nil {
		report.set("unlocked", Unverified, "The lock-state result was incomplete; unlocked state was not established.")
		return report, errors.New("lock-state result did not explicitly establish device state")
	}
	if *lock.Result.PasscodeRequired {
		report.set("unlocked", NeedsSetup, "Phone is locked. Unlock it and keep it awake before Apply.")
		report.set("app_placement", NeedsSetup, "Unlock the matched phone; placement has not been exercised.")
		return report, nil
	}
	report.set("unlocked", Ready, "The phone reported unlocked at this check. Apply rechecks fresh state.")
	report.set("app_placement", Unverified, "Configurator, matching read access and unlocked state were checked. Placement itself was not exercised; supported preferences, a complete proposal and final preflight are still required.")
	return report, nil
}
func matchingIdentity(raw []byte, config Config) error {
	var response struct {
		Output map[string]json.RawMessage `json:"Output"`
		Errors json.RawMessage            `json:"Errors"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return errors.New("invalid Configurator identity response")
	}
	if reportedErrors(response.Errors) || reportedErrors(response.Output["Errors"]) {
		return errors.New("Configurator reported errors; identity response is incomplete")
	}
	found := 0
	matched := false
	for ecid, data := range response.Output {
		if strings.EqualFold(ecid, config.ECID) {
			found++
			var value struct {
				UDID string `json:"UDID"`
			}
			if json.Unmarshal(data, &value) == nil && value.UDID == config.UDID {
				matched = true
			}
		}
	}
	if found != 1 || !matched {
		return errors.New("ECID and UDID do not identify the same verified phone")
	}
	return nil
}
func reportedErrors(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	var has func(any) bool
	has = func(v any) bool {
		switch x := v.(type) {
		case nil:
			return false
		case map[string]any:
			for _, child := range x {
				if has(child) {
					return true
				}
			}
			return false
		case []any:
			return len(x) > 0
		case string:
			return x != ""
		case bool:
			return x
		default:
			return true
		}
	}
	return has(value)
}
