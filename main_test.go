package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// readColorKey
// ---------------------------------------------------------------------------

const validColorsToml = `accent = "#82FB9C"
foreground = "#ddf7ff"
background = "#0B0C16"
`

func writeColorsToml(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "colors.toml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadColorKeyAccent(t *testing.T) {
	p := writeColorsToml(t, validColorsToml)
	got, err := readColorKey("accent", p)
	if err != nil {
		t.Fatal(err)
	}
	want := [3]uint8{130, 251, 156}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestReadColorKeyForeground(t *testing.T) {
	p := writeColorsToml(t, validColorsToml)
	got, err := readColorKey("foreground", p)
	if err != nil {
		t.Fatal(err)
	}
	want := [3]uint8{221, 247, 255}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestReadColorKeyMissingKey(t *testing.T) {
	p := writeColorsToml(t, "foreground = \"#ddf7ff\"\n")
	_, err := readColorKey("accent", p)
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
}

func TestReadColorKeyMissingForeground(t *testing.T) {
	p := writeColorsToml(t, "accent = \"#82FB9C\"\n")
	_, err := readColorKey("foreground", p)
	if err == nil {
		t.Fatal("expected error for missing foreground, got nil")
	}
}

// ---------------------------------------------------------------------------
// applySaturation
// ---------------------------------------------------------------------------

func TestApplySaturationFullPreservesColor(t *testing.T) {
	in := [3]uint8{130, 251, 156}
	got := applySaturation(in, 1.0)
	if got != in {
		t.Errorf("got %v, want %v", got, in)
	}
}

func TestApplySaturationZeroGreyscale(t *testing.T) {
	got := applySaturation([3]uint8{130, 251, 156}, 0.0)
	if got[0] != got[1] || got[1] != got[2] {
		t.Errorf("expected greyscale (all channels equal), got %v", got)
	}
}

func TestApplySaturationClampsAboveOne(t *testing.T) {
	got := applySaturation([3]uint8{100, 200, 150}, 999.0)
	for i, c := range got {
		if c > 255 {
			t.Errorf("channel %d out of range: %d", i, c)
		}
	}
}

// relativeSaturation is the HSV "S" component (0..1) for sRGB rgb, matching
// the max-based definition used inside applySaturation.
func relativeSaturation(rgb [3]uint8) float64 {
	r := float64(rgb[0]) / 255
	g := float64(rgb[1]) / 255
	b := float64(rgb[2]) / 255
	maxC := math.Max(r, math.Max(g, b))
	minC := math.Min(r, math.Min(g, b))
	if maxC == 0 {
		return 0
	}
	return (maxC - minC) / maxC
}

func TestApplySaturationBoostIncreasesVividness(t *testing.T) {
	c := [3]uint8{150, 180, 160}
	one := applySaturation(c, 1.0)
	two := applySaturation(c, 2.0)
	s1 := relativeSaturation(one)
	s2 := relativeSaturation(two)
	if s2 < s1 {
		t.Errorf("boost should not lower saturation: s1=%.4f s2=%.4f", s1, s2)
	}
}

// ---------------------------------------------------------------------------
// sendColorToWLED — using a mock HTTP server
// ---------------------------------------------------------------------------

func wledServer(t *testing.T, statusCode int) (*httptest.Server, func() []byte) {
	t.Helper()
	var last []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last, _ = io.ReadAll(r.Body)
		w.WriteHeader(statusCode)
	}))
	t.Cleanup(func() { srv.Close() })
	return srv, func() []byte { return last }
}

func TestSendColorToWLEDPostsCorrectPayload(t *testing.T) {
	srv, body := wledServer(t, http.StatusOK)
	orig := wledURLOverride
	wledURLOverride = srv.URL + "/json/state"
	t.Cleanup(func() { wledURLOverride = orig })

	if err := sendColorToWLED("ignored", [3]uint8{130, 251, 156}, 200); err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body(), &payload); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if payload["on"] != true {
		t.Error("expected on=true")
	}
	if payload["bri"].(float64) != 200 {
		t.Errorf("expected bri=200, got %v", payload["bri"])
	}
	segs := payload["seg"].([]any)
	col := segs[0].(map[string]any)["col"].([]any)[0].([]any)
	want := []int{130, 251, 156}
	for i, v := range want {
		if int(col[i].(float64)) != v {
			t.Errorf("col[%d]: got %v want %d", i, col[i], v)
		}
	}
}

func TestSendColorToWLEDReturnsErrorOnBadStatus(t *testing.T) {
	srv, _ := wledServer(t, http.StatusInternalServerError)
	orig := wledURLOverride
	wledURLOverride = srv.URL + "/json/state"
	t.Cleanup(func() { wledURLOverride = orig })

	err := sendColorToWLED("ignored", [3]uint8{130, 251, 156}, 255)
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
}

func TestSendSpatialGradientToWLEDPostsPerLEDHex(t *testing.T) {
	srv, body := wledServer(t, http.StatusOK)
	orig := wledURLOverride
	wledURLOverride = srv.URL + "/json/state"
	t.Cleanup(func() { wledURLOverride = orig })

	strip := [][3]uint8{{255, 0, 0}, {0, 255, 0}, {0, 0, 255}}
	if err := sendSpatialGradientToWLED("ignored", strip, 199); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body(), &payload); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if payload["bri"].(float64) != 199 {
		t.Errorf("bri: got %v want 199", payload["bri"])
	}
	seg0 := payload["seg"].([]any)[0].(map[string]any)
	i := seg0["i"].([]any)
	if len(i) != 3 {
		t.Fatalf("seg.i length: got %d want 3", len(i))
	}
	if s := i[0].(string); s != "FF0000" {
		t.Errorf("LED0 hex: got %q want FF0000", s)
	}
	if s := i[1].(string); s != "00FF00" {
		t.Errorf("LED1 hex: got %q want 00FF00", s)
	}
	if s := i[2].(string); s != "0000FF" {
		t.Errorf("LED2 hex: got %q want 0000FF", s)
	}
}

// ---------------------------------------------------------------------------
// pushIfChanged
// ---------------------------------------------------------------------------

type fixedColorSource struct {
	color    [3]uint8
	sentinel string
}

func (s *fixedColorSource) Read() ([3]uint8, error)   { return s.color, nil }
func (s *fixedColorSource) WatchDir() string          { return "/tmp" }
func (s *fixedColorSource) IsTrigger(string) bool     { return true }
func (s *fixedColorSource) Sentinel() (string, error) { return s.sentinel, nil }

func TestPushIfChangedSendsOnFirstCall(t *testing.T) {
	var sent int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	orig := wledURLOverride
	wledURLOverride = srv.URL + "/json/state"
	t.Cleanup(func() { wledURLOverride = orig })

	tracker := &pushTracker{}
	src := &fixedColorSource{color: [3]uint8{100, 150, 200}}
	pushIfChanged(src, tracker, "ignored", 255, 1.0, pushOpts{})
	if sent != 1 {
		t.Errorf("expected 1 send, got %d", sent)
	}
}

func TestPushIfChangedDoesNotResendSameColor(t *testing.T) {
	var sent int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	orig := wledURLOverride
	wledURLOverride = srv.URL + "/json/state"
	t.Cleanup(func() { wledURLOverride = orig })

	tracker := &pushTracker{}
	src := &fixedColorSource{color: [3]uint8{100, 150, 200}}
	pushIfChanged(src, tracker, "ignored", 255, 1.0, pushOpts{})
	pushIfChanged(src, tracker, "ignored", 255, 1.0, pushOpts{})
	if sent != 1 {
		t.Errorf("expected 1 send (deduplicated), got %d", sent)
	}
}

func TestPushIfChangedGradientDedupes(t *testing.T) {
	var sent int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	origURL := wledURLOverride
	wledURLOverride = srv.URL + "/json/state"
	t.Cleanup(func() { wledURLOverride = origURL })

	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 4, 1))
	for x := 0; x < 2; x++ {
		img.Set(x, 0, color.RGBA{R: 255, A: 255})
	}
	for x := 2; x < 4; x++ {
		img.Set(x, 0, color.RGBA{B: 255, A: 255})
	}
	imgPath := filepath.Join(dir, "bg.png")
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	link := filepath.Join(dir, "background")
	if err := os.Symlink(imgPath, link); err != nil {
		t.Fatal(err)
	}

	origOverride := wallpaperPathOverride
	wallpaperPathOverride = link
	t.Cleanup(func() { wallpaperPathOverride = origOverride })

	tracker := &pushTracker{}
	src := &BgColorSource{}
	opts := pushOpts{bgGradient: true, gradientLEDs: 4}
	pushIfChanged(src, tracker, "ignored", 255, 1.0, opts)
	pushIfChanged(src, tracker, "ignored", 255, 1.0, opts)
	if sent != 1 {
		t.Errorf("expected 1 HTTP POST (deduped), got %d", sent)
	}
}

func TestPushIfChangedAppliesSaturation(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	orig := wledURLOverride
	wledURLOverride = srv.URL + "/json/state"
	t.Cleanup(func() { wledURLOverride = orig })

	tracker := &pushTracker{}
	// saturation=0 should make r==g==b
	pushIfChanged(&fixedColorSource{color: [3]uint8{100, 200, 150}}, tracker, "ignored", 255, 0.0, pushOpts{})

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatal(err)
	}
	col := payload["seg"].([]any)[0].(map[string]any)["col"].([]any)[0].([]any)
	r := int(col[0].(float64))
	g := int(col[1].(float64))
	b := int(col[2].(float64))
	if r != g || g != b {
		t.Errorf("expected greyscale after saturation=0, got r=%d g=%d b=%d", r, g, b)
	}
}

// Parity with Python test_poll_uses_provided_state_dict: if we already sent this RGB,
// pollTick must not POST again when the sentinel string updates.
func TestPollTickSkipsSendWhenStateMatchesRead(t *testing.T) {
	var sent int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	orig := wledURLOverride
	wledURLOverride = srv.URL + "/json/state"
	t.Cleanup(func() { wledURLOverride = orig })

	c := [3]uint8{100, 150, 200}
	tracker := &pushTracker{}
	tracker.lastSolid = &c
	src := &fixedColorSource{color: c, sentinel: "s1"}
	var last string
	pollTick("ignored", 255, 1.0, src, tracker, &last, pushOpts{})
	if sent != 0 {
		t.Errorf("want 0 HTTP sends when last_color matches read(), got %d", sent)
	}
}

// ---------------------------------------------------------------------------
// readBgColor
// ---------------------------------------------------------------------------

func TestReadBgColorAveragesLinear(t *testing.T) {
	dir := t.TempDir()

	// 2×2 image: top row red, bottom row blue.
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{R: 255, A: 255})
	img.Set(0, 1, color.RGBA{B: 255, A: 255})
	img.Set(1, 1, color.RGBA{B: 255, A: 255})

	imgPath := filepath.Join(dir, "bg.png")
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = png.Encode(f, img)
	f.Close()

	link := filepath.Join(dir, "background")
	if err := os.Symlink(imgPath, link); err != nil {
		t.Fatal(err)
	}

	got, err := readBgColor(link)
	if err != nil {
		t.Fatal(err)
	}
	if got[1] != 0 {
		t.Errorf("expected green=0, got %d", got[1])
	}
	// Linear-space average of 255 and 0 under γ=2.2 ≈ 180.
	if got[0] < 170 || got[0] > 190 {
		t.Errorf("expected red≈180 (linear avg), got %d", got[0])
	}
	if got[2] < 170 || got[2] > 190 {
		t.Errorf("expected blue≈180 (linear avg), got %d", got[2])
	}
}

// Like test_omarchy_wled.test_read_bg_color_linear_avg_brighter_than_naive on main:
// half red / half black — linear-spot average should be well above naive 127.
func TestWallpaperColumnAverageMapsToLEDs(t *testing.T) {
	dir := t.TempDir()
	// 3×2: three columns (R / G / B); two rows duplicate — column averages stay pure primaries.
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		img.Set(0, y, color.RGBA{R: 255, A: 255})
		img.Set(1, y, color.RGBA{G: 255, A: 255})
		img.Set(2, y, color.RGBA{B: 255, A: 255})
	}
	imgPath := filepath.Join(dir, "cols.png")
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	link := filepath.Join(dir, "background")
	if err := os.Symlink(imgPath, link); err != nil {
		t.Fatal(err)
	}
	colors, err := wallpaperColumnAverageRowColorsForLEDs(link, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(colors) != 3 {
		t.Fatalf("want 3 samples, got %d", len(colors))
	}
	if colors[0][0] <= colors[0][2] {
		t.Errorf("first LED should read redder than blue: %v", colors[0])
	}
	if colors[2][2] <= colors[2][0] {
		t.Errorf("last LED should read bluer than red: %v", colors[2])
	}
}

func TestReadBgGradientHorizontalHalves(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
		for x := 2; x < 4; x++ {
			img.Set(x, y, color.RGBA{B: 255, A: 255})
		}
	}
	imgPath := filepath.Join(dir, "bg.png")
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	link := filepath.Join(dir, "background")
	if err := os.Symlink(imgPath, link); err != nil {
		t.Fatal(err)
	}
	left, right, err := readBgGradientHorizontal(link)
	if err != nil {
		t.Fatal(err)
	}
	if left[0] <= left[2] {
		t.Errorf("expected red-dominant left half, got %v", left)
	}
	if right[2] <= right[0] {
		t.Errorf("expected blue-dominant right half, got %v", right)
	}
}

func TestReadBgColorStripLinearAvgBrighterThanNaive(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{A: 255})
	imgPath := filepath.Join(dir, "bg.png")
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	link := filepath.Join(dir, "background")
	if err := os.Symlink(imgPath, link); err != nil {
		t.Fatal(err)
	}
	got, err := readBgColor(link)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] <= 150 {
		t.Errorf("expected linear avg red > 150 (naive sRGB would be ~127), got %d", got[0])
	}
}

// ---------------------------------------------------------------------------
// makeSource / ColorSource.IsTrigger
// ---------------------------------------------------------------------------

func TestMakeSourceAccent(t *testing.T) {
	src := makeSource("accent")
	ts, ok := src.(*ThemeColorSource)
	if !ok {
		t.Fatalf("expected *ThemeColorSource, got %T", src)
	}
	if ts.tomlColorKey != "accent" {
		t.Errorf("expected tomlColorKey=accent, got %s", ts.tomlColorKey)
	}
}

func TestMakeSourceFg(t *testing.T) {
	src := makeSource("fg")
	ts, ok := src.(*ThemeColorSource)
	if !ok {
		t.Fatalf("expected *ThemeColorSource, got %T", src)
	}
	if ts.tomlColorKey != "foreground" {
		t.Errorf("expected tomlColorKey=foreground, got %s", ts.tomlColorKey)
	}
}

func TestMakeSourceForegroundAlias(t *testing.T) {
	src := makeSource("foreground")
	ts, ok := src.(*ThemeColorSource)
	if !ok {
		t.Fatalf("expected *ThemeColorSource, got %T", src)
	}
	if ts.tomlColorKey != "foreground" {
		t.Errorf("expected tomlColorKey=foreground, got %s", ts.tomlColorKey)
	}
}

func TestMakeSourceBg(t *testing.T) {
	src := makeSource("bg")
	if _, ok := src.(*BgColorSource); !ok {
		t.Fatalf("expected *BgColorSource, got %T", src)
	}
}

func TestThemeSourceIsTriggerOnThemeNameFile(t *testing.T) {
	src := &ThemeColorSource{tomlColorKey: "accent"}
	if !src.IsTrigger(themeNameFile) {
		t.Errorf("expected IsTrigger(%s) to be true", themeNameFile)
	}
}

func TestThemeSourceIsTriggerOnColorsToml(t *testing.T) {
	src := &ThemeColorSource{tomlColorKey: "accent"}
	if !src.IsTrigger(colorsToml) {
		t.Errorf("expected IsTrigger(%s) to be true", colorsToml)
	}
}

func TestBgSourceIsTriggerOnBackgroundLink(t *testing.T) {
	src := &BgColorSource{}
	if !src.IsTrigger(backgroundLink) {
		t.Errorf("expected IsTrigger(%s) to be true", backgroundLink)
	}
	if src.IsTrigger("/some/other/path") {
		t.Error("expected IsTrigger on unrelated path to be false")
	}
}

// ---------------------------------------------------------------------------
// parseArgs / version
// ---------------------------------------------------------------------------

func TestParseArgsVersionShortFlag(t *testing.T) {
	opts, err := parseArgs([]string{"-v"}, map[string]string{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.showVersion {
		t.Fatal("showVersion: want true")
	}
	if opts.wledIP != "" {
		t.Fatalf("wledIP: want empty when only -v, got %q", opts.wledIP)
	}
}

func TestParseArgsVersionLongFlag(t *testing.T) {
	opts, err := parseArgs([]string{"-version"}, map[string]string{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.showVersion {
		t.Fatal("showVersion: want true")
	}
}

func TestParseArgsUnknownFlag(t *testing.T) {
	_, err := parseArgs([]string{"-notreal"}, map[string]string{}, io.Discard)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCliGradientRequiresBg(t *testing.T) {
	if err := validateCli(&cliOpts{bgGradient: true, sourceName: "accent"}); err == nil {
		t.Fatal("expected error when -gradient without bg source")
	}
	if err := validateCli(&cliOpts{bgGradient: true, sourceName: "bg"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsGradientFromConfig(t *testing.T) {
	opts, err := parseArgs([]string{}, map[string]string{
		"gradient":      "true",
		"gradient_leds": "120",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.bgGradient {
		t.Fatal("expected bgGradient from config")
	}
	if opts.gradientLEDs != 120 {
		t.Fatalf("gradientLEDs: got %d want 120", opts.gradientLEDs)
	}
}
