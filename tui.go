package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// tuiStdin is used for TTY checks and the Bubble Tea program (override in tests).
var tuiStdin = os.Stdin

// runTUI starts the configuration TUI (omarchy-wled tui subcommand).
func runTUI() error {
	if !term.IsTerminal(int(tuiStdin.Fd())) {
		return fmt.Errorf("omarchy-wled tui requires a terminal (stdin is not a TTY)")
	}
	cfg, err := loadTuiConfig(configPath)
	if err != nil {
		return err
	}
	p := tea.NewProgram(newTuiModel(cfg), tea.WithAltScreen(), tea.WithInput(tuiStdin))
	_, err = p.Run()
	return err
}

// ---------------------------------------------------------------------------
// Bubble Tea model
// ---------------------------------------------------------------------------

type tuiScreen int

const (
	tuiScreenSetup tuiScreen = iota
	tuiScreenMain
)

type tuiFocus int

const (
	focusIP tuiFocus = iota
	focusSource
	focusBrightness
	focusSaturation
	focusService
	focusSave
	focusQuit
)

const sourceMaxIdx = 2

var sourceLabels = []string{"Accent", "Foreground", "Wallpaper avg"}

func sourceIdxFromName(name string) int {
	switch normalizeSource(name) {
	case "fg":
		return 1
	case "bg":
		return 2
	default:
		return 0
	}
}

func sourceIdxToName(idx int) string {
	switch idx {
	case 1:
		return "fg"
	case 2:
		return "bg"
	default:
		return "accent"
	}
}

type previewTickMsg struct{ gen int }

type serviceActiveMsg struct{ active bool }

type tuiModel struct {
	screen   tuiScreen
	width    int
	setupTI  textinput.Model
	mainTI   textinput.Model
	focus    tuiFocus
	sourceIx int
	brightPct int // 0–100 step 2
	satPct    int // 0–200 step 2 → saturation = satPct/100

	cfg        tuiConfig
	serviceOn  bool
	previewRGB [3]uint8
	previewErr string  // WLED / read errors (cleared on successful preview)
	toast      string  // save / service messages
	toastErr   bool

	previewGen int
	previewArm int

	errSetup string
}

func newTuiModel(initial *tuiConfig) *tuiModel {
	if initial == nil {
		ti := textinput.New()
		ti.Placeholder = "e.g. 192.168.1.50"
		ti.CharLimit = 255
		ti.Focus()
		return &tuiModel{
			screen:  tuiScreenSetup,
			setupTI: ti,
			width:   80,
		}
	}

	mt := textinput.New()
	mt.SetValue(initial.IP)
	mt.CharLimit = 255
	mt.Focus()

	m := &tuiModel{
		screen:    tuiScreenMain,
		mainTI:    mt,
		focus:     focusIP,
		sourceIx:  sourceIdxFromName(initial.Source),
		brightPct: brightness255ToPct(initial.Brightness),
		satPct:    int(initial.Saturation * 100),
		cfg:       *initial,
		width:     80,
	}
	if m.satPct > 200 {
		m.satPct = 200
	}
	if m.satPct < 0 {
		m.satPct = 0
	}
	m.brightPct = clampEven(m.brightPct, 0, 100)
	m.satPct = clampEven(m.satPct, 0, 200)
	return m
}

func clampEven(v, lo, hi int) int {
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	if v%2 != 0 {
		v--
	}
	return v
}

func (m *tuiModel) Init() tea.Cmd {
	if m.screen == tuiScreenSetup {
		return m.setupTI.Focus()
	}
	return tea.Batch(
		textinput.Blink,
		func() tea.Msg {
			sc := newServiceController(m.cfg.IP)
			return serviceActiveMsg{active: sc.IsActive()}
		},
		m.schedulePreview(),
	)
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyMsg:
		k := msg.String()
		if k == "ctrl+c" || k == "esc" {
			return m, tea.Quit
		}

		switch m.screen {
		case tuiScreenSetup:
			return m.updateSetupKey(msg)
		case tuiScreenMain:
			return m.updateMainKey(msg)
		}

	case serviceActiveMsg:
		m.serviceOn = msg.active
		return m, nil

	case previewTickMsg:
		if msg.gen != m.previewArm {
			return m, nil
		}
		return m.runPreview()
	}

	var cmd tea.Cmd
	if m.screen == tuiScreenSetup {
		m.setupTI, cmd = m.setupTI.Update(msg)
		return m, cmd
	}
	if m.focus == focusIP {
		m.mainTI, cmd = m.mainTI.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *tuiModel) updateSetupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		ip := strings.TrimSpace(m.setupTI.Value())
		if ip == "" {
			m.errSetup = "IP address is required."
			return m, nil
		}
		m.errSetup = ""
		m.cfg = tuiConfig{
			IP: ip, Source: tuiDefaultSource, Brightness: tuiDefaultBrightness, Saturation: tuiDefaultSaturation,
		}
		if err := saveTuiConfig(configPath, &m.cfg); err != nil {
			m.errSetup = err.Error()
			return m, nil
		}
		mt := textinput.New()
		mt.SetValue(ip)
		mt.CharLimit = 255
		mt.Focus()
		m.mainTI = mt
		m.screen = tuiScreenMain
		m.focus = focusIP
		m.sourceIx = 0
		m.brightPct = 100
		m.satPct = int(tuiDefaultSaturation * 100)
		return m, tea.Batch(
			textinput.Blink,
			func() tea.Msg {
				sc := newServiceController(m.cfg.IP)
				return serviceActiveMsg{active: sc.IsActive()}
			},
			m.schedulePreview(),
		)
	}
	var cmd tea.Cmd
	m.setupTI, cmd = m.setupTI.Update(msg)
	return m, cmd
}

func (m *tuiModel) updateMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()

	if k == "q" && m.focus != focusIP {
		return m, tea.Quit
	}
	if k == "s" && m.focus != focusIP {
		return m.doSave()
	}

	switch k {
	case "tab":
		m.cycleFocus(1)
		return m, m.syncFocus()
	case "shift+tab":
		m.cycleFocus(-1)
		return m, m.syncFocus()
	}

	if m.focus == focusIP {
		var cmd tea.Cmd
		m.mainTI, cmd = m.mainTI.Update(msg)
		cmd = tea.Batch(cmd, m.schedulePreview())
		return m, cmd
	}

	switch m.focus {
	case focusSource:
		switch k {
		case "left", "h":
			m.sourceIx--
			if m.sourceIx < 0 {
				m.sourceIx = sourceMaxIdx
			}
			return m, m.schedulePreview()
		case "right", "l":
			m.sourceIx++
			if m.sourceIx > sourceMaxIdx {
				m.sourceIx = 0
			}
			return m, m.schedulePreview()
		}
	case focusBrightness:
		switch k {
		case "left", "down", "h":
			m.brightPct = clampEven(m.brightPct-2, 0, 100)
			return m, m.schedulePreview()
		case "right", "up", "l":
			m.brightPct = clampEven(m.brightPct+2, 0, 100)
			return m, m.schedulePreview()
		}
	case focusSaturation:
		switch k {
		case "left", "down", "h":
			m.satPct = clampEven(m.satPct-2, 0, 200)
			return m, m.schedulePreview()
		case "right", "up", "l":
			m.satPct = clampEven(m.satPct+2, 0, 200)
			return m, m.schedulePreview()
		}
	case focusService:
		if k == " " {
			return m.toggleService()
		}
	case focusSave:
		if k == "enter" {
			return m.doSave()
		}
	case focusQuit:
		if k == "enter" {
			return m, tea.Quit
		}
	}

	// space on save = save
	if m.focus == focusSave && k == " " {
		return m.doSave()
	}
	return m, nil
}

func (m *tuiModel) syncFocus() tea.Cmd {
	if m.focus == focusIP {
		return m.mainTI.Focus()
	}
	m.mainTI.Blur()
	return nil
}

func (m *tuiModel) cycleFocus(dir int) {
	// 0..6
	n := int(m.focus)
	n += dir
	if n < 0 {
		n = 6
	}
	if n > 6 {
		n = 0
	}
	m.focus = tuiFocus(n)
}

func (m *tuiModel) currentConfigFromForm() tuiConfig {
	ip := strings.TrimSpace(m.mainTI.Value())
	return tuiConfig{
		IP:         ip,
		Source:     sourceIdxToName(m.sourceIx),
		Brightness: brightnessPctTo255(m.brightPct),
		Saturation: float64(m.satPct) / 100.0,
	}
}

func (m *tuiModel) schedulePreview() tea.Cmd {
	m.previewGen++
	m.previewArm = m.previewGen
	g := m.previewGen
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return previewTickMsg{gen: g}
	})
}

func (m *tuiModel) runPreview() (tea.Model, tea.Cmd) {
	cfg := m.currentConfigFromForm()
	if err := cfg.Validate(); err != nil {
		m.previewErr = err.Error()
		return m, nil
	}
	if err := previewSolidToWLED(cfg.IP, cfg.Source, cfg.Brightness, cfg.Saturation); err != nil {
		m.previewErr = err.Error()
		return m, nil
	}
	m.previewErr = ""
	src := makeSource(normalizeSource(cfg.Source))
	if rgb, err := src.Read(); err == nil {
		m.previewRGB = applySaturation(rgb, cfg.Saturation)
	}
	return m, nil
}

func (m *tuiModel) setToast(msg string, isErr bool) {
	m.toast = msg
	m.toastErr = isErr
}

func (m *tuiModel) doSave() (tea.Model, tea.Cmd) {
	cfg := m.currentConfigFromForm()
	if err := cfg.Validate(); err != nil {
		m.setToast(err.Error(), true)
		return m, nil
	}
	was := newServiceController(cfg.IP).IsActive()
	if err := saveTuiConfig(configPath, &cfg); err != nil {
		m.setToast(err.Error(), true)
		return m, nil
	}
	m.cfg = cfg
	m.previewErr = ""
	if was {
		if err := newServiceController(cfg.IP).Restart(); err != nil {
			m.setToast(fmt.Sprintf("Saved (service restart failed: %v)", err), true)
			return m, nil
		}
		m.setToast("Saved and service restarted.", false)
	} else {
		m.setToast("Configuration saved.", false)
	}
	return m, m.schedulePreview()
}

func (m *tuiModel) toggleService() (tea.Model, tea.Cmd) {
	cfg := m.currentConfigFromForm()
	if err := cfg.Validate(); err != nil {
		m.setToast(err.Error(), true)
		return m, nil
	}
	if strings.TrimSpace(cfg.IP) == "" {
		m.setToast("IP is required to control service.", true)
		return m, nil
	}
	if err := saveTuiConfig(configPath, &cfg); err != nil {
		m.setToast(err.Error(), true)
		return m, nil
	}
	m.cfg = cfg
	m.previewErr = ""
	sc := newServiceController(cfg.IP)
	if m.serviceOn {
		if err := sc.Disable(); err != nil {
			m.setToast(fmt.Sprintf("Service error: %v", err), true)
			return m, nil
		}
		m.serviceOn = false
		m.setToast(fmt.Sprintf("Service stopped and disabled: %s", cfg.IP), false)
		return m, nil
	}
	if err := sc.Enable(); err != nil {
		m.setToast(fmt.Sprintf("Service error: %v", err), true)
		return m, nil
	}
	m.serviceOn = true
	m.setToast(fmt.Sprintf("Settings saved — service started: omarchy-wled → %s", cfg.IP), false)
	return m, nil
}

func (m *tuiModel) View() string {
	if m.width <= 0 {
		m.width = 80
	}
	accent := lipgloss.Color("#82FB9C")
	errColor := lipgloss.Color("#FF6B6B")
	okColor := accent
	title := lipgloss.NewStyle().Foreground(accent).Bold(true).Width(m.width).Align(lipgloss.Center)
	normal := lipgloss.NewStyle().Width(m.width - 4)
	small := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Width(m.width - 4)

	if m.screen == tuiScreenSetup {
		var b strings.Builder
		b.WriteString(title.Render("omarchy-wled") + "\n\n")
		b.WriteString(normal.Render("Welcome — enter your WLED IP or hostname.") + "\n\n")
		b.WriteString(m.setupTI.View() + "\n")
		if m.errSetup != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(errColor).Render(m.errSetup) + "\n")
		}
		b.WriteString(small.Render("Enter: continue · Esc: quit") + "\n")
		return b.String()
	}

	var b strings.Builder
	b.WriteString(title.Render("omarchy-wled · WLED color sync") + "\n\n")
	b.WriteString(lipgloss.NewStyle().Foreground(accent).Bold(true).Render("Configuration") + "\n")
	b.WriteString(m.renderLabeled("WLED IP / Host", m.mainTI.View(), m.focus == focusIP) + "\n")
	b.WriteString(m.renderLabeled("Color source", m.renderSourceRow(), m.focus == focusSource) + "\n")
	b.WriteString(m.renderLabeled(fmt.Sprintf("Brightness: %d%%", m.brightPct), m.renderBar(m.brightPct, 100), m.focus == focusBrightness) + "\n")
	b.WriteString(m.renderLabeled(fmt.Sprintf("Saturation: %.2f×", float64(m.satPct)/100.0), m.renderBar(m.satPct, 200), m.focus == focusSaturation) + "\n\n")

	b.WriteString(lipgloss.NewStyle().Foreground(accent).Bold(true).Render("Service") + "\n")
	svcLabel := "Off"
	if m.serviceOn {
		svcLabel = "On"
	}
	b.WriteString(m.renderLabeled("Auto-start", svcLabel+" (←/→ or Space when focused)", m.focus == focusService) + "\n\n")

	r, g, c := m.previewRGB[0], m.previewRGB[1], m.previewRGB[2]
	hex := fmt.Sprintf("#%02x%02x%02x", r, g, c)
	swatch := lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Bold(true)
	b.WriteString(lipgloss.NewStyle().Foreground(accent).Bold(true).Render("Current color") + "\n")
	b.WriteString(swatch.Render(fmt.Sprintf("  rgb(%d, %d, %d)  %s", r, g, c, hex)) + "\n\n")

	saveBtn := "[ Save ]"
	quitBtn := "[ Quit ]"
	if m.focus == focusSave {
		saveBtn = lipgloss.NewStyle().Reverse(true).Render(saveBtn)
	}
	if m.focus == focusQuit {
		quitBtn = lipgloss.NewStyle().Reverse(true).Render(quitBtn)
	}
	b.WriteString(lipgloss.NewStyle().Width(m.width).Align(lipgloss.Center).Render(saveBtn+"  "+quitBtn) + "\n")

	statStyle := lipgloss.NewStyle().Width(m.width).Align(lipgloss.Center)
	switch {
	case m.previewErr != "":
		b.WriteString(statStyle.Foreground(errColor).Render(m.previewErr) + "\n")
	case m.toast != "":
		if m.toastErr {
			b.WriteString(statStyle.Foreground(errColor).Render(m.toast) + "\n")
		} else {
			b.WriteString(statStyle.Foreground(okColor).Render(m.toast) + "\n")
		}
	}
	b.WriteString(small.Render("tab/shift+tab focus · ←/→ adjust · space service · s save · q quit") + "\n")
	return b.String()
}

func (m *tuiModel) renderLabeled(label, field string, focused bool) string {
	st := lipgloss.NewStyle().Width(22)
	l := st.Render(label + ":")
	body := field
	if focused {
		body = lipgloss.NewStyle().Reverse(true).Render(field)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, l, " ", body)
}

func (m *tuiModel) renderSourceRow() string {
	parts := make([]string, len(sourceLabels))
	for i, lab := range sourceLabels {
		mark := " "
		if i == m.sourceIx {
			mark = "■"
		}
		parts[i] = fmt.Sprintf("%s %s", mark, lab)
	}
	return strings.Join(parts, "  ")
}

func (m *tuiModel) renderBar(val, max int) string {
	w := 24
	filled := val * w / max
	if filled > w {
		filled = w
	}
	var sb strings.Builder
	for i := 0; i < w; i++ {
		if i < filled {
			sb.WriteRune('█')
		} else {
			sb.WriteRune('░')
		}
	}
	return sb.String()
}
