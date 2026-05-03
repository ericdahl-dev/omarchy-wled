package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTuiConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := &tuiConfig{IP: "10.0.0.1", Source: "fg", Brightness: 128, Saturation: 0.8}
	if err := saveTuiConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := loadTuiConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected config")
	}
	if got.IP != cfg.IP || got.Source != "fg" || got.Brightness != cfg.Brightness || got.Saturation != cfg.Saturation {
		t.Errorf("got %+v, want %+v", got, cfg)
	}
}

func TestLoadTuiConfigMissing(t *testing.T) {
	got, err := loadTuiConfig(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestTuiConfigValidation(t *testing.T) {
	if err := (&tuiConfig{IP: "1", Source: "accent", Brightness: 255, Saturation: 1.2}).Validate(); err != nil {
		t.Errorf("valid: %v", err)
	}
	if err := (&tuiConfig{IP: "", Source: "accent", Brightness: 255, Saturation: 1.0}).Validate(); err == nil {
		t.Error("empty ip")
	}
	if err := (&tuiConfig{IP: "1", Source: "bogus", Brightness: 255, Saturation: 1.0}).Validate(); err == nil {
		t.Error("bad source")
	}
}

func TestNormalizeSourceRoundTrip(t *testing.T) {
	if g := normalizeSource("foreground"); g != "fg" {
		t.Errorf("got %q", g)
	}
}

func TestBrightnessPct(t *testing.T) {
	if brightness255ToPct(255) != 100 {
		t.Errorf("100%%")
	}
	if brightnessPctTo255(50) != 128 && brightnessPctTo255(50) != 127 {
		// 50% of 255 = 127.5 -> 128
	}
	if b := brightnessPctTo255(50); b < 127 || b > 128 {
		t.Errorf("50%% -> %d", b)
	}
}

func TestPreviewSolidToWLED(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	root := t.TempDir()
	colorsPath := filepath.Join(root, "colors.toml")
	if err := os.WriteFile(colorsPath, []byte(`accent = "#82FB9C"
foreground = "#ddf7ff"
background = "#0B0C16"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	origColors := colorsToml
	colorsToml = colorsPath
	t.Cleanup(func() { colorsToml = origColors })

	origWLED := wledURLOverride
	wledURLOverride = srv.URL + "/json/state"
	t.Cleanup(func() { wledURLOverride = origWLED })

	if err := previewSolidToWLED("127.0.0.1", "accent", 255, 1.0); err != nil {
		t.Fatal(err)
	}
}

func TestServiceEnableWritesExecStart(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var calls [][]string
	testSystemctl = func(name string, arg ...string) int {
		calls = append(calls, append([]string{name}, arg...))
		return 0
	}
	t.Cleanup(func() { testSystemctl = nil })

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}

	sc := newServiceController("192.168.1.1")
	if err := sc.Enable(); err != nil {
		t.Fatal(err)
	}

	unitPath := filepath.Join(tmp, ".config/systemd/user/omarchy-wled.service")
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ExecStart="+exe) {
		t.Errorf("unit missing ExecStart=%s:\n%s", exe, data)
	}

	found := false
	for _, c := range calls {
		if len(c) >= 4 && c[0] == "systemctl" && c[1] == "--user" && c[2] == "enable" && c[3] == "--now" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected enable --now in %#v", calls)
	}
}

func TestServiceDisableCommand(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var last []string
	testSystemctl = func(name string, arg ...string) int {
		last = append([]string{name}, arg...)
		return 0
	}
	t.Cleanup(func() { testSystemctl = nil })

	sc := newServiceController("10.0.0.1")
	if err := sc.Disable(); err != nil {
		t.Fatal(err)
	}
	want := []string{"systemctl", "--user", "disable", "--now", "omarchy-wled"}
	if len(last) != len(want) {
		t.Fatalf("got %#v", last)
	}
	for i := range want {
		if last[i] != want[i] {
			t.Errorf("idx %d: got %q want %q", i, last[i], want[i])
		}
	}
}
