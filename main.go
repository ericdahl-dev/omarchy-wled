// Command omarchy-wled syncs Omarchy accent/wallpaper color to a WLED device.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// version is set at link time: go build -ldflags "-X main.version=v1.2.3"
var version = "dev"

// ---------------------------------------------------------------------------
// Runtime paths (resolved from $HOME at startup)
// ---------------------------------------------------------------------------

var (
	homeDir        = os.Getenv("HOME")
	colorsToml     = filepath.Join(homeDir, ".config/omarchy/current/theme/colors.toml")
	themeNameFile  = filepath.Join(homeDir, ".config/omarchy/current/theme.name")
	backgroundLink = filepath.Join(homeDir, ".config/omarchy/current/background")
	configPath     = filepath.Join(homeDir, ".config/omarchy-wled/config.toml")
)

// ---------------------------------------------------------------------------
// Color reading
// ---------------------------------------------------------------------------

// colorsTomlKeyHexPattern matches lines like: accent = "#82FB9C"
var colorsTomlKeyHexPattern = regexp.MustCompile(`(?m)^(\w+)\s*=\s*"#([0-9a-fA-F]{6})"`)

// readColorKey reads one named sRGB color from an Omarchy colors.toml file.
func readColorKey(tomlKey, filePath string) ([3]uint8, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return [3]uint8{}, err
	}
	for _, m := range colorsTomlKeyHexPattern.FindAllStringSubmatch(string(data), -1) {
		if m[1] == tomlKey {
			hex := m[2]
			r, _ := strconv.ParseUint(hex[0:2], 16, 8)
			g, _ := strconv.ParseUint(hex[2:4], 16, 8)
			b, _ := strconv.ParseUint(hex[4:6], 16, 8)
			return [3]uint8{uint8(r), uint8(g), uint8(b)}, nil
		}
	}
	return [3]uint8{}, fmt.Errorf("%s color not found in %s", tomlKey, filePath)
}

// ---------------------------------------------------------------------------
// Color transformation
// ---------------------------------------------------------------------------

// applySaturation scales the HSV saturation of an sRGB color.
// saturationMultiplier 1.0 = unchanged, 0.0 = grey, >1.0 = more vivid (S clamped to 1).
func applySaturation(rgb [3]uint8, saturationMultiplier float64) [3]uint8 {
	rN := float64(rgb[0]) / 255
	gN := float64(rgb[1]) / 255
	bN := float64(rgb[2]) / 255

	maxChannel := math.Max(rN, math.Max(gN, bN))
	minChannel := math.Min(rN, math.Min(gN, bN))
	chroma := maxChannel - minChannel

	value := maxChannel
	saturation := 0.0
	if maxChannel > 0 {
		saturation = chroma / maxChannel
	}

	hue := 0.0
	if chroma > 0 {
		switch maxChannel {
		case rN:
			hue = (gN - bN) / chroma
			if gN < bN {
				hue += 6
			}
		case gN:
			hue = (bN-rN)/chroma + 2
		default:
			hue = (rN-gN)/chroma + 4
		}
		hue /= 6
	}

	saturation = math.Max(0, math.Min(1, saturation*saturationMultiplier))
	return hsvToRGB(hue, saturation, value)
}

func hsvToRGB(h, s, v float64) [3]uint8 {
	if s == 0 {
		c := uint8(math.Round(v * 255))
		return [3]uint8{c, c, c}
	}
	h6 := h * 6
	if h6 >= 6 {
		h6 = 0
	}
	i := int(h6)
	f := h6 - float64(i)
	p := v * (1 - s)
	q := v * (1 - s*f)
	t := v * (1 - s*(1-f))
	var r, g, b float64
	switch i {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	return [3]uint8{
		uint8(math.Round(r * 255)),
		uint8(math.Round(g * 255)),
		uint8(math.Round(b * 255)),
	}
}

// ---------------------------------------------------------------------------
// ColorSource protocol
// ---------------------------------------------------------------------------

// ColorSource describes anything that can produce a color and identify which
// filesystem path to watch for changes.
type ColorSource interface {
	Read() ([3]uint8, error)
	WatchDir() string
	IsTrigger(path string) bool
	Sentinel() (string, error)
}

// ThemeColorSource reads a named color key (accent, foreground, …) from
// the Omarchy theme's colors.toml.
type ThemeColorSource struct{ tomlColorKey string }

func (s *ThemeColorSource) Read() ([3]uint8, error) {
	return readColorKey(s.tomlColorKey, colorsToml)
}

func (s *ThemeColorSource) WatchDir() string {
	return filepath.Dir(themeNameFile)
}

func (s *ThemeColorSource) IsTrigger(eventPath string) bool {
	absEvent, _ := filepath.Abs(eventPath)
	absThemeName, _ := filepath.Abs(themeNameFile)
	if absEvent == absThemeName {
		return true
	}
	absColorsToml, _ := filepath.Abs(colorsToml)
	return absEvent == absColorsToml
}

// Sentinel combines theme.name and colors.toml mtimes so poll/watch react when
// either changes (both live under ~/.config/omarchy/current/).
func (s *ThemeColorSource) Sentinel() (string, error) {
	themeNameInfo, err := os.Stat(themeNameFile)
	if err != nil {
		return "", err
	}
	fingerprint := strconv.FormatInt(themeNameInfo.ModTime().UnixNano(), 10)
	if colorsInfo, err := os.Stat(colorsToml); err == nil {
		fingerprint += ":" + strconv.FormatInt(colorsInfo.ModTime().UnixNano(), 10)
	}
	return fingerprint, nil
}

// BgColorSource computes the average color of the current Omarchy wallpaper.
type BgColorSource struct{}

func (s *BgColorSource) Read() ([3]uint8, error) {
	return readBgColor(wallpaperSymlink())
}

func (s *BgColorSource) WatchDir() string {
	return filepath.Dir(wallpaperSymlink())
}

func (s *BgColorSource) IsTrigger(eventPath string) bool {
	absEvent, _ := filepath.Abs(eventPath)
	absBackgroundSymlink, _ := filepath.Abs(wallpaperSymlink())
	return absEvent == absBackgroundSymlink
}

func (s *BgColorSource) Sentinel() (string, error) {
	wallpaperTargetPath, err := os.Readlink(wallpaperSymlink())
	if err != nil {
		return "", err
	}
	return wallpaperTargetPath, nil
}

// makeSource maps CLI/config names to the concrete ColorSource implementation.
func makeSource(sourceName string) ColorSource {
	switch sourceName {
	case "bg":
		return &BgColorSource{}
	case "fg", "foreground":
		return &ThemeColorSource{tomlColorKey: "foreground"}
	default:
		return &ThemeColorSource{tomlColorKey: "accent"}
	}
}

// ---------------------------------------------------------------------------
// Push logic
// ---------------------------------------------------------------------------

// pushOpts carries WLED push behavior that is not part of ColorSource.
type pushOpts struct {
	bgGradient   bool
	gradientLEDs int // 0 = GET /json/info for strip length (spatial fade only)
}

// gradientSent records last gradient strip for deduplication.
type gradientSent struct {
	colors [][3]uint8
}

func dupGradientColors(src [][3]uint8) [][3]uint8 {
	out := make([][3]uint8, len(src))
	copy(out, src)
	return out
}

func gradientSlicesEqual(a, b [][3]uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pushTracker remembers the last solid or gradient push so we skip duplicate POSTs.
type pushTracker struct {
	lastSolid    *[3]uint8
	lastGradient *gradientSent
}

func (t *pushTracker) shouldSendSolid(rgb [3]uint8) bool {
	if t.lastGradient != nil {
		return true
	}
	if t.lastSolid == nil {
		return true
	}
	return *t.lastSolid != rgb
}

func (t *pushTracker) markSolid(rgb [3]uint8) {
	c := rgb
	t.lastSolid = &c
	t.lastGradient = nil
}

func (t *pushTracker) shouldSendGradient(colors [][3]uint8) bool {
	if t.lastSolid != nil {
		return true
	}
	if t.lastGradient == nil {
		return true
	}
	return !gradientSlicesEqual(t.lastGradient.colors, colors)
}

func (t *pushTracker) markGradient(colors [][3]uint8) {
	t.lastGradient = &gradientSent{colors: dupGradientColors(colors)}
	t.lastSolid = nil
}

// pushIfChanged reads the current color, applies saturation, and sends it to
// WLED only if it differs from the last push. Silently skips read errors
// (e.g. theme files not yet present).
func pushIfChanged(src ColorSource, tracker *pushTracker, wledIP string, brightness int, saturation float64, opts pushOpts) {
	if opts.bgGradient {
		if _, ok := src.(*BgColorSource); !ok {
			return
		}
		n, err := resolveGradientLEDCount(wledIP, opts.gradientLEDs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return
		}
		stripRGB, err := wallpaperColumnAverageRowColorsForLEDs(wallpaperSymlink(), n)
		if err != nil {
			return
		}
		for i := range stripRGB {
			stripRGB[i] = applySaturation(stripRGB[i], saturation)
		}
		if !tracker.shouldSendGradient(stripRGB) {
			return
		}
		tracker.markGradient(stripRGB)
		if err := sendSpatialGradientToWLED(wledIP, stripRGB, brightness); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return
		}
		a, z := stripRGB[0], stripRGB[len(stripRGB)-1]
		fmt.Printf("Updated WLED → gradient strip rgb%v…%v bri=%d sat=%.2f leds=%d\n",
			a, z, brightness, saturation, n)
		return
	}

	rgb, err := src.Read()
	if err != nil {
		return
	}
	rgb = applySaturation(rgb, saturation)
	if !tracker.shouldSendSolid(rgb) {
		return
	}
	tracker.markSolid(rgb)
	if err := sendColorToWLED(wledIP, rgb, brightness); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	fmt.Printf("Updated WLED → rgb%v bri=%d sat=%.2f\n", rgb, brightness, saturation)
}

// ---------------------------------------------------------------------------
// Watching (inotify via fsnotify)
// ---------------------------------------------------------------------------

// watchSource watches the directory reported by src.WatchDir() and calls
// pushIfChanged whenever src.IsTrigger fires.
func watchSource(wledIP string, brightness int, saturation float64, src ColorSource, opts pushOpts) error {
	tracker := &pushTracker{}
	pushIfChanged(src, tracker, wledIP, brightness, saturation, opts)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("cannot create watcher: %w", err)
	}
	defer watcher.Close()

	watchDirectory := src.WatchDir()
	if err := watcher.Add(watchDirectory); err != nil {
		return fmt.Errorf("cannot watch %s: %w", watchDirectory, err)
	}

	fmt.Fprintf(os.Stderr, "Watching %s for changes — Ctrl-C to stop\n", watchDirectory)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if src.IsTrigger(event.Name) {
				time.Sleep(200 * time.Millisecond)
				pushIfChanged(src, tracker, wledIP, brightness, saturation, opts)
			}
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "Watch error: %v\n", watchErr)
		}
	}
}

// pollTick runs when the ColorSource sentinel string changes (theme fingerprint
// or wallpaper path). Used by poll loop and tests.
func pollTick(wledIP string, brightness int, saturation float64, src ColorSource, tracker *pushTracker, previousSentinel *string, opts pushOpts) {
	currentSentinel, err := src.Sentinel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	if currentSentinel == *previousSentinel {
		return
	}
	*previousSentinel = currentSentinel
	time.Sleep(200 * time.Millisecond)
	pushIfChanged(src, tracker, wledIP, brightness, saturation, opts)
}

// poll is the fsnotify fallback: cheap sleep loop comparing Sentinel() strings.
func poll(wledIP string, brightness int, saturation float64, src ColorSource, opts pushOpts) {
	tracker := &pushTracker{}
	var previousSentinel string
	for {
		pollTick(wledIP, brightness, saturation, src, tracker, &previousSentinel, opts)
		time.Sleep(time.Second)
	}
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

var flatTomlConfigLinePattern = regexp.MustCompile(`(?m)^(\w+)\s*=\s*(.+)$`)

// loadConfig reads ~/.config/omarchy-wled/config.toml and returns a flat
// string map. The format is intentionally simple (no arrays or nested tables)
// so we parse it with a regex instead of pulling in a TOML library.
func parseTomlBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "true" || s == "1" || s == "yes"
}

func loadConfig() map[string]string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return map[string]string{}
	}
	cfg := map[string]string{}
	for _, m := range flatTomlConfigLinePattern.FindAllStringSubmatch(string(data), -1) {
		val := strings.TrimSpace(m[2])
		val = strings.Trim(val, `"`)
		cfg[m[1]] = val
	}
	return cfg
}

// cliOpts holds parsed flags and positional args after parseArgs.
type cliOpts struct {
	sourceName string
	brightness int
	// saturationOrUnset is the -saturation flag value; -1 means “pick default from source.”
	saturationOrUnset float64
	once              bool
	showVersion       bool
	wledIP            string
	bgGradient     bool
	gradientLEDs   int
}

// parseArgs parses argv using the same defaults as loadConfig merge rules.
func parseArgs(args []string, cfg map[string]string, output io.Writer) (*cliOpts, error) {
	defaultSource := cfg["source"]
	if defaultSource == "" {
		defaultSource = "accent"
	}
	defaultBrightness := 255
	if v, ok := cfg["brightness"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			defaultBrightness = n
		}
	}
	defaultSaturation := -1.0
	if v, ok := cfg["saturation"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			defaultSaturation = f
		}
	}
	defaultGradient := false
	if v, ok := cfg["gradient"]; ok && parseTomlBool(v) {
		defaultGradient = true
	}
	defaultGradientLEDs := 0
	if v, ok := cfg["gradient_leds"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			defaultGradientLEDs = n
		}
	}

	fs := flag.NewFlagSet("omarchy-wled", flag.ContinueOnError)
	fs.SetOutput(output)

	opts := &cliOpts{}
	fs.BoolVar(&opts.showVersion, "v", false, "Print version and exit")
	fs.BoolVar(&opts.showVersion, "version", false, "Print version and exit")

	sourceName := fs.String("source", defaultSource,
		"Color source: accent (default), fg (foreground), or bg (wallpaper average)")
	brightness := fs.Int("brightness", defaultBrightness,
		"LED brightness 0-255 (default 255)")
	saturationFlag := fs.Float64("saturation", defaultSaturation,
		"Saturation multiplier (0.0=greyscale, 1.0=unchanged, >1.0=boost;\n"+
			"default 1.2 for accent, 1.0 for fg/bg)")
	gradient := fs.Bool("gradient", defaultGradient,
		"Wallpaper left/right averages as strip endpoints (spatial fade via seg.i; requires -source bg)")
	gradientLEDs := fs.Int("gradient-leds", defaultGradientLEDs,
		"LED count for spatial fade (0 = fetch from WLED /json/info)")
	once := fs.Bool("once", false, "Send current color once and exit (no watching)")
	fs.Usage = func() {
		fmt.Fprintf(output, "Usage: omarchy-wled [options] [WLED_IP]\n\nOptions:\n")
		fs.PrintDefaults()
	}

	err := fs.Parse(args)
	if err != nil {
		return nil, err
	}

	opts.sourceName = *sourceName
	opts.brightness = *brightness
	opts.saturationOrUnset = *saturationFlag
	opts.once = *once
	opts.bgGradient = *gradient
	opts.gradientLEDs = *gradientLEDs

	opts.wledIP = cfg["ip"]
	if fs.NArg() > 0 {
		opts.wledIP = fs.Arg(0)
	}
	return opts, nil
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func validateCli(opts *cliOpts) error {
	if opts.bgGradient && opts.sourceName != "bg" {
		return fmt.Errorf("-gradient requires -source bg")
	}
	return nil
}

func main() {
	cfg := loadConfig()
	opts, err := parseArgs(os.Args[1:], cfg, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
	}

	if opts.showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	if opts.wledIP == "" {
		fmt.Fprintln(os.Stderr,
			"error: WLED IP is required (pass as argument or set ip in ~/.config/omarchy-wled/config.toml)")
		os.Exit(1)
	}

	if err := validateCli(opts); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	effectiveSaturation := opts.saturationOrUnset
	if effectiveSaturation < 0 {
		if opts.sourceName == "accent" {
			effectiveSaturation = 1.2
		} else {
			effectiveSaturation = 1.0
		}
	}

	src := makeSource(opts.sourceName)
	pushOpts := pushOpts{bgGradient: opts.bgGradient, gradientLEDs: opts.gradientLEDs}

	if opts.once {
		tracker := &pushTracker{}
		pushIfChanged(src, tracker, opts.wledIP, opts.brightness, effectiveSaturation, pushOpts)
		return
	}

	if err := watchSource(opts.wledIP, opts.brightness, effectiveSaturation, src, pushOpts); err != nil {
		fmt.Fprintf(os.Stderr, "Watch failed: %v — falling back to polling\n", err)
		poll(opts.wledIP, opts.brightness, effectiveSaturation, src, pushOpts)
	}
}
