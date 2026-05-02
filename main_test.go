package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// ---------------------------------------------------------------------------
// pushIfChanged
// ---------------------------------------------------------------------------

type fixedColorSource struct {
	color    [3]uint8
	sentinel string
}

func (s *fixedColorSource) Read() ([3]uint8, error)  { return s.color, nil }
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

	st := &state{}
	src := &fixedColorSource{color: [3]uint8{100, 150, 200}}
	pushIfChanged(src, st, "ignored", 255, 1.0)
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

	st := &state{}
	src := &fixedColorSource{color: [3]uint8{100, 150, 200}}
	pushIfChanged(src, st, "ignored", 255, 1.0)
	pushIfChanged(src, st, "ignored", 255, 1.0)
	if sent != 1 {
		t.Errorf("expected 1 send (deduplicated), got %d", sent)
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

	st := &state{}
	// saturation=0 should make r==g==b
	pushIfChanged(&fixedColorSource{color: [3]uint8{100, 200, 150}}, st, "ignored", 255, 0.0)

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

// ---------------------------------------------------------------------------
// makeSource / ColorSource.IsTrigger
// ---------------------------------------------------------------------------

func TestMakeSourceAccent(t *testing.T) {
	src := makeSource("accent")
	ts, ok := src.(*ThemeColorSource)
	if !ok {
		t.Fatalf("expected *ThemeColorSource, got %T", src)
	}
	if ts.key != "accent" {
		t.Errorf("expected key=accent, got %s", ts.key)
	}
}

func TestMakeSourceFg(t *testing.T) {
	src := makeSource("fg")
	ts, ok := src.(*ThemeColorSource)
	if !ok {
		t.Fatalf("expected *ThemeColorSource, got %T", src)
	}
	if ts.key != "foreground" {
		t.Errorf("expected key=foreground, got %s", ts.key)
	}
}

func TestMakeSourceBg(t *testing.T) {
	src := makeSource("bg")
	if _, ok := src.(*BgColorSource); !ok {
		t.Fatalf("expected *BgColorSource, got %T", src)
	}
}

func TestThemeSourceIsTriggerOnThemeNameFile(t *testing.T) {
	src := &ThemeColorSource{key: "accent"}
	if !src.IsTrigger(themeNameFile) {
		t.Errorf("expected IsTrigger(%s) to be true", themeNameFile)
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
