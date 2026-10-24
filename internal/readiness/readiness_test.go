package readiness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T) (Checker, Config, *[][]string) {
	t.Helper()
	root := t.TempDir()
	cfg := filepath.Join(root, "cfgutil")
	if err := os.WriteFile(cfg, []byte("synthetic"), 0700); err != nil {
		t.Fatal(err)
	}
	calls := [][]string{}
	checker := Checker{LookPath: func(string) (string, error) { return "/synthetic/xcrun", nil }, Glob: func(string) ([]string, error) { return []string{"synthetic.xctestrun"}, nil }}
	checker.Run = func(ctx context.Context, args []string) ([]byte, []byte, error) {
		calls = append(calls, append([]string{}, args...))
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--find") {
			return []byte("/synthetic/devicectl\n"), nil, nil
		}
		if strings.Contains(joined, "get UDID") {
			return []byte(`{"Output":{"0x1":{"UDID":"synthetic"},"Errors":{"0x1":{}}}}`), nil, nil
		}
		if strings.Contains(joined, "lockState") {
			for i, arg := range args {
				if arg == "--json-output" {
					return nil, nil, os.WriteFile(args[i+1], []byte(`{"info":{"outcome":"success"},"result":{"passcodeRequired":false}}`), 0600)
				}
			}
		}
		return nil, nil, errors.New("unexpected command")
	}
	return checker, Config{Root: root, Cfgutil: cfg, UDID: "synthetic", ECID: "0x1"}, &calls
}
func TestInspectAndOfflineCheckNeverRunCommands(t *testing.T) {
	checker, config, calls := fixture(t)
	report := checker.Inspect(config)
	if len(*calls) != 0 || report.Checked {
		t.Fatal("inspection contacted phone")
	}
	if report.Find("runner").Status != Unverified || report.Find("xctest_runtime").Status != Unverified {
		t.Fatal("file presence claimed runtime ready")
	}
	config.UDID = ""
	report, err := checker.Check(context.Background(), config)
	if err != nil || len(*calls) != 0 || report.Checked {
		t.Fatal(err, *calls)
	}
	if report.Find("wizard").Status != Ready {
		t.Fatal("offline wizard unavailable")
	}
	if row := report.Find("app_placement"); row.Status != NeedsSetup || !strings.Contains(row.Detail, "Local planning remains available") {
		t.Fatal("missing live target not reflected in app readiness", row)
	}
}
func TestSuccessfulReadChecksNeverClaimWritesOrXCTestReady(t *testing.T) {
	checker, config, calls := fixture(t)
	report, err := checker.Check(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cfgutil", "apple_tools", "identity", "unlocked"} {
		if report.Find(key).Status != Ready {
			t.Fatal(key, report.Find(key))
		}
	}
	for _, key := range []string{"app_placement", "page_removal", "runner", "xctest_runtime"} {
		if report.Find(key).Status != Unverified {
			t.Fatal("overclaimed", key, report.Find(key))
		}
	}
	for _, key := range []string{"widget_movement", "bottom_alignment", "reserved_space"} {
		if report.Find(key).Status != Unsupported {
			t.Fatal(key)
		}
	}
	if len(*calls) != 3 {
		t.Fatal(*calls)
	}
	for _, command := range *calls {
		joined := strings.Join(command, " ")
		for _, forbidden := range []string{"set-icon-layout", "test-without-building", "remove", "install", "python"} {
			if strings.Contains(joined, forbidden) {
				t.Fatal("mutating command", command)
			}
		}
	}
	if !report.Checked || report.CheckedAt == "" {
		t.Fatal("missing observation time")
	}
}
func TestMismatchAndPartialIdentityStopBeforeLockRead(t *testing.T) {
	for _, raw := range []string{`{"Output":{"0x1":{"UDID":"another"}}}`, `{"Output":{"0x1":{"UDID":"synthetic"},"Errors":{"0x1":{"reason":"partial"}}}}`, `{}`} {
		t.Run(raw, func(t *testing.T) {
			checker, config, calls := fixture(t)
			original := checker.Run
			checker.Run = func(ctx context.Context, args []string) ([]byte, []byte, error) {
				if args[len(args)-1] == "UDID" {
					*calls = append(*calls, args)
					return []byte(raw), nil, nil
				}
				return original(ctx, args)
			}
			report, err := checker.Check(context.Background(), config)
			if err == nil || len(*calls) != 2 || report.Find("identity").Status == Ready {
				t.Fatal(err, *calls, report)
			}
			if row := report.Find("app_placement"); row.Status != NeedsSetup || !strings.Contains(row.Detail, "Local planning remains available") {
				t.Fatal("unverified pairing not reflected in app readiness", row)
			}
		})
	}
}
func TestLockStatusRequiresExplicitSuccessAndFalse(t *testing.T) {
	for _, test := range []struct {
		raw       string
		status    Status
		wantError bool
	}{{`{"info":{"outcome":"success"},"result":{"passcodeRequired":true}}`, NeedsSetup, false}, {`{"info":{"outcome":"success"},"result":{}}`, Unverified, true}, {`{"info":{"outcome":"failed"},"result":{"passcodeRequired":false}}`, Unverified, true}} {
		t.Run(test.raw, func(t *testing.T) {
			checker, config, _ := fixture(t)
			original := checker.Run
			checker.Run = func(ctx context.Context, args []string) ([]byte, []byte, error) {
				for i, arg := range args {
					if arg == "--json-output" {
						return nil, nil, os.WriteFile(args[i+1], []byte(test.raw), 0600)
					}
				}
				return original(ctx, args)
			}
			report, err := checker.Check(context.Background(), config)
			if (err != nil) != test.wantError || report.Find("unlocked").Status != test.status {
				t.Fatal(err, report.Find("unlocked"))
			}
			if report.Find("app_placement").Status == Ready {
				t.Fatal("placement overclaimed")
			}
		})
	}
}
func TestMissingToolsAndCancelledChecksDoNotDispatch(t *testing.T) {
	checker, config, calls := fixture(t)
	config.Cfgutil = filepath.Join(t.TempDir(), "missing")
	report, err := checker.Check(context.Background(), config)
	if err == nil || len(*calls) != 0 || report.Find("cfgutil").Status != NeedsSetup {
		t.Fatal(err, *calls)
	}
	checker, config, calls = fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = checker.Check(ctx, config)
	if !errors.Is(err, context.Canceled) || len(*calls) != 0 {
		t.Fatal(err, *calls)
	}
}
func TestIdentityArgumentsAreLiteral(t *testing.T) {
	checker, config, calls := fixture(t)
	config.UDID = "literal;$(not-a-command)"
	_, _ = checker.Check(context.Background(), config)
	want := []string{config.Cfgutil, "--format", "JSON", "--ecid", config.ECID, "--timeout", "15", "get", "UDID"}
	if len(*calls) < 2 || !reflect.DeepEqual((*calls)[1], want) {
		t.Fatal(*calls)
	}
}
