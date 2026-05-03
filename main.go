// Command omarchy-wled syncs Omarchy accent/wallpaper color to a WLED device.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	xdraw "golang.org/x/image/draw" // High-quality resampling (e.g. Catmull-Rom) for wallpaper 1×1 average.
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

// wallpaperSrgbToLinearByte and wallpaperLinearToSrgbByte implement the same γ=2.2
// pipeline as Python omarchy_wled.read_bg_color (PIL .point() LUTs). Values stay in 0–255.
var wallpaperSrgbToLinearByte, wallpaperLinearToSrgbByte [256]uint8

func init() {
	initWallpaperGammaLookupTables()
}

func initWallpaperGammaLookupTables() {
	for channel := 0; channel < 256; channel++ {
		v := float64(channel)
		wallpaperSrgbToLinearByte[channel] = uint8(math.Round(math.Pow(v/255.0, 2.2) * 255.0))
		wallpaperLinearToSrgbByte[channel] = uint8(math.Round(math.Pow(v/255.0, 1.0/2.2) * 255.0))
	}
}

// readBgColor returns the wallpaper “average” color in true display space.
// Steps: decode → γ-decode each channel via LUT → high-quality downscale to 1×1
// (weighted average, same role as PIL LANCZOS) → γ-encode back to sRGB bytes.
func readBgColor(wallpaperSymlinkPath string) ([3]uint8, error) {
	resolvedImagePath, err := filepath.EvalSymlinks(wallpaperSymlinkPath)
	if err != nil {
		return [3]uint8{}, fmt.Errorf("cannot resolve wallpaper symlink: %w", err)
	}

	file, err := os.Open(resolvedImagePath)
	if err != nil {
		return [3]uint8{}, err
	}
	defer file.Close()

	decoded, _, err := image.Decode(file)
	if err != nil {
		return [3]uint8{}, fmt.Errorf("cannot decode image %s: %w", resolvedImagePath, err)
	}

	bounds := decoded.Bounds()
	if bounds.Dx()*bounds.Dy() == 0 {
		return [3]uint8{}, fmt.Errorf("empty image")
	}

	rgbaWorking := image.NewRGBA(bounds)
	draw.Draw(rgbaWorking, bounds, decoded, bounds.Min, draw.Src)

	// Each RGB channel: sRGB byte → linear-light proxy byte (still one byte per channel).
	pixels := rgbaWorking.Pix
	for i := 0; i < len(pixels); i += 4 {
		pixels[i+0] = wallpaperSrgbToLinearByte[pixels[i+0]]
		pixels[i+1] = wallpaperSrgbToLinearByte[pixels[i+1]]
		pixels[i+2] = wallpaperSrgbToLinearByte[pixels[i+2]]
	}

	onePixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	xdraw.CatmullRom.Scale(onePixel, onePixel.Bounds(), rgbaWorking, bounds, draw.Src, nil)

	linearAverage := onePixel.RGBAAt(0, 0)
	return [3]uint8{
		wallpaperLinearToSrgbByte[linearAverage.R],
		wallpaperLinearToSrgbByte[linearAverage.G],
		wallpaperLinearToSrgbByte[linearAverage.B],
	}, nil
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
// WLED transport
// ---------------------------------------------------------------------------

// wledURLOverride can be set in tests to redirect requests to a local server.
// When empty (the default), sendColorToWLED constructs the real WLED URL.
var wledURLOverride string

// sendColorToWLED pushes a solid color to a WLED device via its JSON API.
func sendColorToWLED(ip string, rgb [3]uint8, brightness int) error {
	url := wledURLOverride
	if url == "" {
		url = "http://" + ip + "/json/state"
	}
	payload, err := json.Marshal(map[string]any{
		"on":  true,
		"bri": max(0, min(255, brightness)),
		"seg": []map[string]any{
			{"col": [][]int{{int(rgb[0]), int(rgb[1]), int(rgb[2])}}},
		},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMultiStatus {
		return fmt.Errorf("WLED returned HTTP %d", resp.StatusCode)
	}
	return nil
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
	return readBgColor(backgroundLink)
}

func (s *BgColorSource) WatchDir() string {
	return filepath.Dir(backgroundLink)
}

func (s *BgColorSource) IsTrigger(eventPath string) bool {
	absEvent, _ := filepath.Abs(eventPath)
	absBackgroundSymlink, _ := filepath.Abs(backgroundLink)
	return absEvent == absBackgroundSymlink
}

func (s *BgColorSource) Sentinel() (string, error) {
	wallpaperTargetPath, err := os.Readlink(backgroundLink)
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

// pushTracker remembers the last RGB sent to WLED so we skip duplicate POSTs.
type pushTracker struct{ lastSentRGB *[3]uint8 }

// pushIfChanged reads the current color, applies saturation, and sends it to
// WLED only if it differs from the last push. Silently skips read errors
// (e.g. theme files not yet present).
func pushIfChanged(src ColorSource, tracker *pushTracker, wledIP string, brightness int, saturation float64) {
	rgb, err := src.Read()
	if err != nil {
		return
	}
	rgb = applySaturation(rgb, saturation)
	if tracker.lastSentRGB != nil && *tracker.lastSentRGB == rgb {
		return
	}
	tracker.lastSentRGB = &rgb
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
func watchSource(wledIP string, brightness int, saturation float64, src ColorSource) error {
	tracker := &pushTracker{}
	pushIfChanged(src, tracker, wledIP, brightness, saturation)

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
				pushIfChanged(src, tracker, wledIP, brightness, saturation)
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
func pollTick(wledIP string, brightness int, saturation float64, src ColorSource, tracker *pushTracker, previousSentinel *string) {
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
	pushIfChanged(src, tracker, wledIP, brightness, saturation)
}

// poll is the fsnotify fallback: cheap sleep loop comparing Sentinel() strings.
func poll(wledIP string, brightness int, saturation float64, src ColorSource) {
	tracker := &pushTracker{}
	var previousSentinel string
	for {
		pollTick(wledIP, brightness, saturation, src, tracker, &previousSentinel)
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

	opts.wledIP = cfg["ip"]
	if fs.NArg() > 0 {
		opts.wledIP = fs.Arg(0)
	}
	return opts, nil
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

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

	effectiveSaturation := opts.saturationOrUnset
	if effectiveSaturation < 0 {
		if opts.sourceName == "accent" {
			effectiveSaturation = 1.2
		} else {
			effectiveSaturation = 1.0
		}
	}

	src := makeSource(opts.sourceName)

	if opts.once {
		tracker := &pushTracker{}
		pushIfChanged(src, tracker, opts.wledIP, opts.brightness, effectiveSaturation)
		return
	}

	if err := watchSource(opts.wledIP, opts.brightness, effectiveSaturation, src); err != nil {
		fmt.Fprintf(os.Stderr, "Watch failed: %v — falling back to polling\n", err)
		poll(opts.wledIP, opts.brightness, effectiveSaturation, src)
	}
}
