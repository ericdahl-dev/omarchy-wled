package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// tuiConfig matches ~/.config/omarchy-wled/config.toml fields used by the TUI.
type tuiConfig struct {
	IP         string
	Source     string
	Brightness int
	Saturation float64
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
	m := parseFlatConfigToml(data)
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
	return &tuiConfig{
		IP:         ip,
		Source:     normalizeSource(src),
		Brightness: bri,
		Saturation: sat,
	}, nil
}

func parseFlatConfigToml(data []byte) map[string]string {
	cfg := map[string]string{}
	for _, m := range flatTomlConfigLinePattern.FindAllStringSubmatch(string(data), -1) {
		val := strings.TrimSpace(m[2])
		val = strings.Trim(val, `"`)
		cfg[m[1]] = val
	}
	return cfg
}

// saveTuiConfig writes flat TOML (same shape as Python save_config).
func saveTuiConfig(path string, c *tuiConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	src := normalizeSource(c.Source)
	content := fmt.Sprintf(
		`ip = "%s"
source = "%s"
brightness = %d
saturation = %g
`,
		c.IP, src, c.Brightness, c.Saturation,
	)
	return os.WriteFile(path, []byte(content), 0o644)
}

// previewSolidToWLED reads the color source and pushes a solid color (TUI live preview).
func previewSolidToWLED(ip, sourceName string, brightness255 int, saturation float64) error {
	src := makeSource(normalizeSource(sourceName))
	rgb, err := src.Read()
	if err != nil {
		return err
	}
	rgb = applySaturation(rgb, saturation)
	return sendColorToWLED(ip, rgb, brightness255)
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
	// Remove legacy instance symlink if present (old omarchy-wled@IP template).
	legacy := filepath.Join(userHome(), userSystemdDir, "default.target.wants", fmt.Sprintf("omarchy-wled@%s.service", s.ip))
	_ = os.Remove(legacy)

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

func (s *serviceController) Enable() error {
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
