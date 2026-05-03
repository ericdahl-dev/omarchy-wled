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

// wallpaperPathOverride, when non-empty (tests), is used instead of backgroundLink
// for wallpaper symlink resolution.
var wallpaperPathOverride string

func wallpaperSymlink() string {
	if wallpaperPathOverride != "" {
		return wallpaperPathOverride
	}
	return backgroundLink
}

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

// wallpaperAverageFromRGBA applies the same γ pipeline as readBgColor on an RGBA
// buffer (copy): linearize → Catmull-Rom 1×1 → encode back to sRGB bytes.
func wallpaperAverageFromRGBA(rgba *image.RGBA) ([3]uint8, error) {
	bounds := rgba.Bounds()
	if bounds.Dx()*bounds.Dy() == 0 {
		return [3]uint8{}, fmt.Errorf("empty image")
	}
	work := image.NewRGBA(bounds)
	draw.Draw(work, bounds, rgba, bounds.Min, draw.Src)
	pixels := work.Pix
	for i := 0; i < len(pixels); i += 4 {
		pixels[i+0] = wallpaperSrgbToLinearByte[pixels[i+0]]
		pixels[i+1] = wallpaperSrgbToLinearByte[pixels[i+1]]
		pixels[i+2] = wallpaperSrgbToLinearByte[pixels[i+2]]
	}
	onePixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	xdraw.CatmullRom.Scale(onePixel, onePixel.Bounds(), work, bounds, draw.Src, nil)
	linearAverage := onePixel.RGBAAt(0, 0)
	return [3]uint8{
		wallpaperLinearToSrgbByte[linearAverage.R],
		wallpaperLinearToSrgbByte[linearAverage.G],
		wallpaperLinearToSrgbByte[linearAverage.B],
	}, nil
}

func cropRGBA(src *image.RGBA, r image.Rectangle) *image.RGBA {
	r = r.Intersect(src.Bounds())
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
	return dst
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
	return wallpaperAverageFromRGBA(rgbaWorking)
}

// readBgGradientHorizontal returns linear-space averages for the left and right
// halves of the wallpaper (split at the horizontal midline).
func readBgGradientHorizontal(wallpaperSymlinkPath string) (left, right [3]uint8, err error) {
	resolvedImagePath, err := filepath.EvalSymlinks(wallpaperSymlinkPath)
	if err != nil {
		return left, right, fmt.Errorf("cannot resolve wallpaper symlink: %w", err)
	}
	file, err := os.Open(resolvedImagePath)
	if err != nil {
		return left, right, err
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	if err != nil {
		return left, right, fmt.Errorf("cannot decode image %s: %w", resolvedImagePath, err)
	}
	bounds := decoded.Bounds()
	if bounds.Dx()*bounds.Dy() == 0 {
		return left, right, fmt.Errorf("empty image")
	}
	rgbaFull := image.NewRGBA(bounds)
	draw.Draw(rgbaFull, bounds, decoded, bounds.Min, draw.Src)
	if bounds.Dx() < 2 {
		c, err := wallpaperAverageFromRGBA(rgbaFull)
		return c, c, err
	}
	midX := bounds.Min.X + bounds.Dx()/2
	leftRect := image.Rect(bounds.Min.X, bounds.Min.Y, midX, bounds.Max.Y)
	rightRect := image.Rect(midX, bounds.Min.Y, bounds.Max.X, bounds.Max.Y)
	left, err = wallpaperAverageFromRGBA(cropRGBA(rgbaFull, leftRect))
	if err != nil {
		return left, right, err
	}
	right, err = wallpaperAverageFromRGBA(cropRGBA(rgbaFull, rightRect))
	return left, right, err
}

// wallpaperCenterRowColorsForLEDs samples the horizontal midline of the wallpaper,
// applies the same γ LUT as readBgColor, then Catmull-Rom rescales that row to
// exactly ledCount pixels (one true-color value per strip LED).
func wallpaperCenterRowColorsForLEDs(wallpaperSymlinkPath string, ledCount int) ([][3]uint8, error) {
	if ledCount <= 0 {
		return nil, fmt.Errorf("ledCount must be positive")
	}
	resolvedImagePath, err := filepath.EvalSymlinks(wallpaperSymlinkPath)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve wallpaper symlink: %w", err)
	}
	file, err := os.Open(resolvedImagePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("cannot decode image %s: %w", resolvedImagePath, err)
	}
	bounds := decoded.Bounds()
	if bounds.Dx()*bounds.Dy() == 0 {
		return nil, fmt.Errorf("empty image")
	}
	rowY := bounds.Min.Y + bounds.Dy()/2
	rowW := bounds.Dx()
	rowRgba := image.NewRGBA(image.Rect(0, 0, rowW, 1))
	draw.Draw(rowRgba, rowRgba.Bounds(), decoded, image.Point{X: bounds.Min.X, Y: rowY}, draw.Src)

	pix := rowRgba.Pix
	for i := 0; i < len(pix); i += 4 {
		pix[i+0] = wallpaperSrgbToLinearByte[pix[i+0]]
		pix[i+1] = wallpaperSrgbToLinearByte[pix[i+1]]
		pix[i+2] = wallpaperSrgbToLinearByte[pix[i+2]]
	}

	outStrip := image.NewRGBA(image.Rect(0, 0, ledCount, 1))
	xdraw.CatmullRom.Scale(outStrip, outStrip.Bounds(), rowRgba, rowRgba.Bounds(), draw.Src, nil)

	out := make([][3]uint8, ledCount)
	for x := 0; x < ledCount; x++ {
		c := outStrip.RGBAAt(x, 0)
		out[x] = [3]uint8{
			wallpaperLinearToSrgbByte[c.R],
			wallpaperLinearToSrgbByte[c.G],
			wallpaperLinearToSrgbByte[c.B],
		}
	}
	return out, nil
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

// maxGradientLEDChunk is the most colors WLED recommends per /json/state POST for seg.i.
const maxGradientLEDChunk = 256

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

// wledJSONURL builds http(s)://host/json/<name> honoring wledURLOverride in tests.
func wledJSONURL(ip string, name string) string {
	if wledURLOverride != "" {
		base := strings.TrimSuffix(wledURLOverride, "/json/state")
		return base + "/json/" + name
	}
	return "http://" + ip + "/json/" + name
}

var (
	cachedGradientLEDCount    int
	cachedGradientLEDCountFor string
)

// fetchWLEDLEDCount reads GET /json/info and returns leds.count.
func fetchWLEDLEDCount(ip string) (int, error) {
	url := wledJSONURL(ip, "info")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("WLED /json/info returned HTTP %d", resp.StatusCode)
	}
	var info struct {
		Leds struct {
			Count int `json:"count"`
		} `json:"leds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return 0, err
	}
	if info.Leds.Count <= 0 {
		return 0, fmt.Errorf("WLED reported invalid LED count")
	}
	return info.Leds.Count, nil
}

func resolveGradientLEDCount(ip string, configured int) (int, error) {
	if configured > 0 {
		return configured, nil
	}
	if cachedGradientLEDCount > 0 && cachedGradientLEDCountFor == ip {
		return cachedGradientLEDCount, nil
	}
	n, err := fetchWLEDLEDCount(ip)
	if err != nil {
		return 0, err
	}
	cachedGradientLEDCount = n
	cachedGradientLEDCountFor = ip
	return n, nil
}

func rgbToHex(r, g, b uint8) string {
	return fmt.Sprintf("%02X%02X%02X", r, g, b)
}

// sendSpatialGradientToWLED paints per-LED colors using seg[].i (one entry per physical LED).
func sendSpatialGradientToWLED(ip string, stripRGB [][3]uint8, brightness int) error {
	if len(stripRGB) == 0 {
		return fmt.Errorf("no strip colors")
	}
	hexes := make([]string, len(stripRGB))
	for i, rgb := range stripRGB {
		hexes[i] = rgbToHex(rgb[0], rgb[1], rgb[2])
	}
	url := wledJSONURL(ip, "state")
	bri := max(0, min(255, brightness))

	for offset := 0; offset < len(hexes); offset += maxGradientLEDChunk {
		end := min(offset+maxGradientLEDChunk, len(hexes))
		chunk := hexes[offset:end]
		var iArr []any
		if offset > 0 {
			iArr = append(iArr, offset)
		}
		for _, h := range chunk {
			iArr = append(iArr, h)
		}
		payload, err := json.Marshal(map[string]any{
			"on":  true,
			"bri": bri,
			"seg": []map[string]any{
				{"i": iArr},
			},
		})
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMultiStatus {
			return fmt.Errorf("WLED returned HTTP %d", resp.StatusCode)
		}
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
		stripRGB, err := wallpaperCenterRowColorsForLEDs(wallpaperSymlink(), n)
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
