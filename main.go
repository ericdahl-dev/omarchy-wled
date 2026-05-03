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

var hexLineRe = regexp.MustCompile(`(?m)^(\w+)\s*=\s*"#([0-9a-fA-F]{6})"`)

// readColorKey extracts a named hex color from a colors.toml file.
func readColorKey(key, path string) ([3]uint8, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [3]uint8{}, err
	}
	for _, m := range hexLineRe.FindAllStringSubmatch(string(data), -1) {
		if m[1] == key {
			hex := m[2]
			r, _ := strconv.ParseUint(hex[0:2], 16, 8)
			g, _ := strconv.ParseUint(hex[2:4], 16, 8)
			b, _ := strconv.ParseUint(hex[4:6], 16, 8)
			return [3]uint8{uint8(r), uint8(g), uint8(b)}, nil
		}
	}
	return [3]uint8{}, fmt.Errorf("%s color not found in %s", key, path)
}

// readBgColor computes the perceptual (linear-light) average color of the
// current Omarchy wallpaper image. The symlink at backgroundLink is resolved
// to the actual image file, which must be a format supported by Go's image
// stdlib (PNG, JPEG).
func readBgColor(linkPath string) ([3]uint8, error) {
	imgPath, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		return [3]uint8{}, fmt.Errorf("cannot resolve wallpaper symlink: %w", err)
	}

	f, err := os.Open(imgPath)
	if err != nil {
		return [3]uint8{}, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return [3]uint8{}, fmt.Errorf("cannot decode image %s: %w", imgPath, err)
	}

	bounds := img.Bounds()
	var sumR, sumG, sumB float64
	n := (bounds.Max.X - bounds.Min.X) * (bounds.Max.Y - bounds.Min.Y)
	if n == 0 {
		return [3]uint8{}, fmt.Errorf("empty image")
	}

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			rr, gg, bb, _ := img.At(x, y).RGBA() // 16-bit channels
			sumR += srgbToLinear(uint8(rr >> 8))
			sumG += srgbToLinear(uint8(gg >> 8))
			sumB += srgbToLinear(uint8(bb >> 8))
		}
	}

	fn := float64(n)
	return [3]uint8{
		linearToSrgb(sumR / fn),
		linearToSrgb(sumG / fn),
		linearToSrgb(sumB / fn),
	}, nil
}

func srgbToLinear(v uint8) float64 {
	return math.Pow(float64(v)/255.0, 2.2)
}

func linearToSrgb(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(math.Round(math.Pow(v, 1.0/2.2) * 255))
}

// ---------------------------------------------------------------------------
// Color transformation
// ---------------------------------------------------------------------------

// applySaturation scales the HSV saturation of an RGB color.
// 1.0 = unchanged, 0.0 = greyscale, >1.0 = boost (clamped to [0, 1]).
func applySaturation(rgb [3]uint8, scale float64) [3]uint8 {
	r := float64(rgb[0]) / 255
	g := float64(rgb[1]) / 255
	b := float64(rgb[2]) / 255

	maxC := math.Max(r, math.Max(g, b))
	minC := math.Min(r, math.Min(g, b))
	delta := maxC - minC

	v := maxC
	s := 0.0
	if maxC > 0 {
		s = delta / maxC
	}

	h := 0.0
	if delta > 0 {
		switch maxC {
		case r:
			h = (g - b) / delta
			if g < b {
				h += 6
			}
		case g:
			h = (b-r)/delta + 2
		default:
			h = (r-g)/delta + 4
		}
		h /= 6
	}

	s = math.Max(0, math.Min(1, s*scale))
	return hsvToRGB(h, s, v)
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
type ThemeColorSource struct{ key string }

func (s *ThemeColorSource) Read() ([3]uint8, error) {
	return readColorKey(s.key, colorsToml)
}

func (s *ThemeColorSource) WatchDir() string {
	return filepath.Dir(themeNameFile)
}

func (s *ThemeColorSource) IsTrigger(path string) bool {
	a, _ := filepath.Abs(path)
	tn, _ := filepath.Abs(themeNameFile)
	if a == tn {
		return true
	}
	ct, _ := filepath.Abs(colorsToml)
	return a == ct
}

// Sentinel combines theme.name and colors.toml mtimes so poll/watch react when
// either changes (same directory events can arrive for either file).
func (s *ThemeColorSource) Sentinel() (string, error) {
	tfi, err := os.Stat(themeNameFile)
	if err != nil {
		return "", err
	}
	out := strconv.FormatInt(tfi.ModTime().UnixNano(), 10)
	if cfi, err := os.Stat(colorsToml); err == nil {
		out += ":" + strconv.FormatInt(cfi.ModTime().UnixNano(), 10)
	}
	return out, nil
}

// BgColorSource computes the average color of the current Omarchy wallpaper.
type BgColorSource struct{}

func (s *BgColorSource) Read() ([3]uint8, error) {
	return readBgColor(backgroundLink)
}

func (s *BgColorSource) WatchDir() string {
	return filepath.Dir(backgroundLink)
}

func (s *BgColorSource) IsTrigger(path string) bool {
	a, _ := filepath.Abs(path)
	b, _ := filepath.Abs(backgroundLink)
	return a == b
}

func (s *BgColorSource) Sentinel() (string, error) {
	target, err := os.Readlink(backgroundLink)
	if err != nil {
		return "", err
	}
	return target, nil
}

// makeSource constructs the appropriate ColorSource from a CLI name.
func makeSource(name string) ColorSource {
	switch name {
	case "bg":
		return &BgColorSource{}
	case "fg", "foreground":
		return &ThemeColorSource{key: "foreground"}
	default:
		return &ThemeColorSource{key: "accent"}
	}
}

// ---------------------------------------------------------------------------
// Push logic
// ---------------------------------------------------------------------------

type state struct{ lastColor *[3]uint8 }

// pushIfChanged reads the current color, applies saturation, and sends it to
// WLED only if it differs from the last push. Silently skips read errors
// (e.g. theme files not yet present).
func pushIfChanged(src ColorSource, st *state, ip string, brightness int, saturation float64) {
	color, err := src.Read()
	if err != nil {
		return
	}
	color = applySaturation(color, saturation)
	if st.lastColor != nil && *st.lastColor == color {
		return
	}
	st.lastColor = &color
	if err := sendColorToWLED(ip, color, brightness); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	fmt.Printf("Updated WLED → rgb%v bri=%d sat=%.2f\n", color, brightness, saturation)
}

// ---------------------------------------------------------------------------
// Watching (inotify via fsnotify)
// ---------------------------------------------------------------------------

// watchSource watches the directory reported by src.WatchDir() and calls
// pushIfChanged whenever src.IsTrigger fires.
func watchSource(ip string, brightness int, saturation float64, src ColorSource) error {
	st := &state{}
	pushIfChanged(src, st, ip, brightness, saturation)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("cannot create watcher: %w", err)
	}
	defer w.Close()

	dir := src.WatchDir()
	if err := w.Add(dir); err != nil {
		return fmt.Errorf("cannot watch %s: %w", dir, err)
	}

	fmt.Fprintf(os.Stderr, "Watching %s for changes — Ctrl-C to stop\n", dir)

	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if src.IsTrigger(ev.Name) {
				time.Sleep(200 * time.Millisecond)
				pushIfChanged(src, st, ip, brightness, saturation)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "Watch error: %v\n", err)
		}
	}
}

// poll is a 1-second loop fallback used when fsnotify is unavailable. In the
// Go version fsnotify is always available (it is a compiled-in dependency),
// so this path is only reached on platforms where inotify is unsupported.
func poll(ip string, brightness int, saturation float64, src ColorSource) {
	st := &state{}
	var lastSentinel string
	for {
		sentinel, err := src.Sentinel()
		if err == nil && sentinel != lastSentinel {
			lastSentinel = sentinel
			time.Sleep(200 * time.Millisecond)
			pushIfChanged(src, st, ip, brightness, saturation)
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		time.Sleep(time.Second)
	}
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

var configLineRe = regexp.MustCompile(`(?m)^(\w+)\s*=\s*(.+)$`)

// loadConfig reads ~/.config/omarchy-wled/config.toml and returns a flat
// string map. The format is intentionally simple (no arrays or nested tables)
// so we parse it with a regex instead of pulling in a TOML library.
func loadConfig() map[string]string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return map[string]string{}
	}
	cfg := map[string]string{}
	for _, m := range configLineRe.FindAllStringSubmatch(string(data), -1) {
		val := strings.TrimSpace(m[2])
		val = strings.Trim(val, `"`)
		cfg[m[1]] = val
	}
	return cfg
}

// cliOpts holds parsed flags and positional args after parseArgs.
type cliOpts struct {
	sourceName   string
	brightness   int
	saturationIn float64 // from flag; -1 means use per-source default
	once         bool
	showVersion  bool
	wledIP       string
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
	opts.saturationIn = *saturationFlag
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

	saturation := opts.saturationIn
	if saturation < 0 {
		if opts.sourceName == "accent" {
			saturation = 1.2
		} else {
			saturation = 1.0
		}
	}

	src := makeSource(opts.sourceName)

	if opts.once {
		st := &state{}
		pushIfChanged(src, st, opts.wledIP, opts.brightness, saturation)
		return
	}

	if err := watchSource(opts.wledIP, opts.brightness, saturation, src); err != nil {
		fmt.Fprintf(os.Stderr, "Watch failed: %v — falling back to polling\n", err)
		poll(opts.wledIP, opts.brightness, saturation, src)
	}
}
