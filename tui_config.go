package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ericdahl-dev/omarchy-wled/internal/color"
	"github.com/ericdahl-dev/omarchy-wled/internal/config"
	"github.com/ericdahl-dev/omarchy-wled/internal/daemon"
	"github.com/ericdahl-dev/omarchy-wled/internal/source"
	"github.com/ericdahl-dev/omarchy-wled/internal/wled"
)

// tuiConfig matches ~/.config/omarchy-wled/config.toml fields used by the TUI.
type tuiConfig struct {
	IP           string
	Source       string
	Brightness   int
	Saturation   float64
	Gradient     bool // wallpaper column strip (requires source bg)
	GradientLEDs int  // 0 = fetch LED count from WLED /json/info
}

const (
	tuiDefaultSource     = "accent"
	tuiDefaultBrightness = 255
	tuiDefaultSaturation = 1.2
)

// normalizeSource maps foreground→fg and validates allowed names.
func normalizeSource(s string) string {
	s = strings.TrimSpace(s)
	if s == "foreground" {
		return "fg"
	}
	return s
}

// Validate checks TUI config fields (same rules as Python TuiConfig).
func (c *tuiConfig) Validate() error {
	if strings.TrimSpace(c.IP) == "" {
		return fmt.Errorf("ip must not be empty")
	}
	switch normalizeSource(c.Source) {
	case "accent", "fg", "bg":
	default:
		return fmt.Errorf("source must be accent, fg, foreground, or bg — got %q", c.Source)
	}
	if c.Brightness < 0 || c.Brightness > 255 {
		return fmt.Errorf("brightness must be 0-255 — got %d", c.Brightness)
	}
	if c.Saturation < 0 {
		return fmt.Errorf("saturation must be >= 0 — got %g", c.Saturation)
	}
	if c.Gradient && normalizeSource(c.Source) != "bg" {
		return fmt.Errorf("gradient requires wallpaper (bg) source")
	}
	if c.GradientLEDs < 0 {
		return fmt.Errorf("gradient_leds must be >= 0 — got %d", c.GradientLEDs)
	}
	return nil
}

func brightnessPctTo255(pct int) int {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return int(float64(pct)/100.0*255 + 0.5)
}

func brightness255ToPct(b int) int {
	if b < 0 {
		b = 0
	}
	if b > 255 {
		b = 255
	}
	return int(float64(b)/255.0*100 + 0.5)
}

// loadTuiConfig reads config from path. Returns (nil, nil) if the file is missing.
func loadTuiConfig(path string) (*tuiConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	m := config.ParseFlatBytes(data)
	if len(m) == 0 {
		return nil, nil
	}
	ip := m["ip"]
	if ip == "" {
		return nil, nil
	}
	src := m["source"]
	if src == "" {
		src = tuiDefaultSource
	}
	bri := tuiDefaultBrightness
	if v, ok := m["brightness"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			bri = n
		}
	}
	sat := tuiDefaultSaturation
	if v, ok := m["saturation"]; ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			sat = f
		}
	}
	grad := false
	if v, ok := m["gradient"]; ok && config.ParseBool(v) {
		grad = true
	}
	gradLEDs := 0
	if v, ok := m["gradient_leds"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			gradLEDs = n
		}
	}
	nsrc := normalizeSource(src)
	if nsrc != "bg" {
		grad = false
	}
	return &tuiConfig{
		IP:           ip,
		Source:       nsrc,
		Brightness:   bri,
		Saturation:   sat,
		Gradient:     grad,
		GradientLEDs: gradLEDs,
	}, nil
}

// saveTuiConfig writes flat TOML (same shape as Python save_config).
func saveTuiConfig(path string, c *tuiConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	src := normalizeSource(c.Source)
	grad := c.Gradient && src == "bg"
	content := fmt.Sprintf(
		`ip = "%s"
source = "%s"
brightness = %d
saturation = %g
gradient = %t
gradient_leds = %d
`,
		c.IP, src, c.Brightness, c.Saturation, grad, c.GradientLEDs,
	)
	return os.WriteFile(path, []byte(content), 0o644)
}

// previewPushTUI sends live preview to WLED (solid or wallpaper gradient), using the same
// prepare + dedupe + deliver path as the daemon.
func previewPushTUI(cfg *tuiConfig, tracker *daemon.DedupeTracker) error {
	src := source.FromFlag(normalizeSource(cfg.Source))
	opts := daemon.PushOptions{
		WallpaperGradientStrip: cfg.Gradient && normalizeSource(cfg.Source) == "bg",
		GradientLEDCountOrZero: cfg.GradientLEDs,
	}
	prep, skip, err := daemon.PreparePushColors(src, cfg.IP, cfg.Saturation, opts, true, true)
	if err != nil {
		return err
	}
	return daemon.DeliverPreparedColors(cfg.IP, cfg.Brightness, cfg.Saturation, tracker, prep, skip, false)
}

// previewSolidToWLED reads the color source and pushes a solid color (tests).
func previewSolidToWLED(ip, sourceName string, brightness255 int, saturation float64) error {
	src := source.FromFlag(normalizeSource(sourceName))
	rgb, err := src.Read()
	if err != nil {
		return err
	}
	rgb = color.ScaleSaturation(rgb, saturation)
	return wled.PostSolidJSON(ip, rgb, brightness255)
}

// ---------------------------------------------------------------------------
// systemd user unit (same intent as Python ServiceController; ExecStart is resolved)
// ---------------------------------------------------------------------------

const userSystemdDir = ".config/systemd/user"

// systemctlRunner runs a systemctl command; swap in tests.
type systemctlRunner func(name string, arg ...string) int

func defaultSystemctlRunner() systemctlRunner {
	return func(name string, arg ...string) int {
		cmd := exec.Command(name, arg...)
		cmd.Stdout = nil
		cmd.Stderr = nil
		err := cmd.Run()
		if err == nil {
			return 0
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
}

// serviceController manages the static user unit omarchy-wled.service.
type serviceController struct {
	ip     string
	system systemctlRunner
}

// userHome returns $HOME at call time (for tests that t.Setenv("HOME", ...)).
func userHome() string {
	h := os.Getenv("HOME")
	if h == "" {
		return "/"
	}
	return h
}

// testSystemctl overrides systemctl for tests (nil = real systemctl).
var testSystemctl systemctlRunner

func newServiceController(ip string) *serviceController {
	run := defaultSystemctlRunner()
	if testSystemctl != nil {
		run = testSystemctl
	}
	return &serviceController{ip: ip, system: run}
}

func (s *serviceController) installUnitFile(execPath string) error {
	dir := filepath.Join(userHome(), userSystemdDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	unit := filepath.Join(dir, "omarchy-wled.service")
	body := fmt.Sprintf(`[Unit]
Description=Sync Omarchy theme color to WLED
After=network.target graphical-session.target
PartOf=graphical-session.target

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, execPath)
	if err := os.WriteFile(unit, []byte(body), 0o644); err != nil {
		return err
	}
	_ = s.system("systemctl", "--user", "daemon-reload")
	return nil
}

func (s *serviceController) unitName() string { return "omarchy-wled" }

// cleanupLegacyOmarchyWledUserUnits removes leftover systemd user units from older setups:
// - omarchy-wled@*.service template instances (AUR/README copy-paste)
// - a static omarchy-wled.service whose ExecStart clearly runs the Python stack
func (s *serviceController) cleanupLegacyOmarchyWledUserUnits() {
	base := filepath.Join(userHome(), userSystemdDir)
	for _, pattern := range []string{
		filepath.Join(base, "default.target.wants", "omarchy-wled@*.service"),
		filepath.Join(base, "omarchy-wled@*.service"),
	} {
		matches, _ := filepath.Glob(pattern)
		for _, path := range matches {
			unit := strings.TrimSuffix(filepath.Base(path), ".service")
			_ = s.system("systemctl", "--user", "disable", "--now", unit)
			_ = os.Remove(path)
		}
	}
	path := filepath.Join(base, "omarchy-wled.service")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if !staticUnitExecStartLooksLikeLegacyPython(data) {
		return
	}
	_ = s.system("systemctl", "--user", "disable", "--now", "omarchy-wled")
	_ = os.Remove(path)
}

func staticUnitExecStartLooksLikeLegacyPython(unit []byte) bool {
	for _, line := range strings.Split(string(unit), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		v := strings.ToLower(line)
		if !strings.HasPrefix(v, "execstart=") {
			continue
		}
		return strings.Contains(v, "python") ||
			strings.Contains(v, "pipx") ||
			strings.Contains(v, "pip ") ||
			strings.Contains(v, "uv run") ||
			strings.Contains(v, "omarchy-wled-tui") ||
			strings.Contains(v, "omarchy_wled")
	}
	return false
}

func (s *serviceController) Enable() error {
	s.cleanupLegacyOmarchyWledUserUnits()

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	if err := s.installUnitFile(exe); err != nil {
		return err
	}
	if s.system("systemctl", "--user", "enable", "--now", s.unitName()) != 0 {
		return fmt.Errorf("systemctl enable --now failed")
	}
	return nil
}

func (s *serviceController) Disable() error {
	if s.system("systemctl", "--user", "disable", "--now", s.unitName()) != 0 {
		return fmt.Errorf("systemctl disable --now failed")
	}
	return nil
}

func (s *serviceController) Restart() error {
	if s.system("systemctl", "--user", "restart", s.unitName()) != 0 {
		return fmt.Errorf("systemctl restart failed")
	}
	return nil
}

func (s *serviceController) IsActive() bool {
	return s.system("systemctl", "--user", "is-active", "--quiet", s.unitName()) == 0
}
