package daemon_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ericdahl-dev/omarchy-wled/internal/daemon"
	"github.com/ericdahl-dev/omarchy-wled/internal/source"
	"github.com/ericdahl-dev/omarchy-wled/internal/transport"
	"github.com/ericdahl-dev/omarchy-wled/internal/wled"
)

func TestPreparePushColors_GradientRequiresWallpaperStrict(t *testing.T) {
	src := source.FromFlag("accent")
	tr := &transport.WLEDTransport{IP: "127.0.0.1"}
	_, _, err := daemon.PreparePushColors(src, tr, 1.0, daemon.PushOptions{WallpaperGradientStrip: true}, true, true)
	if err == nil {
		t.Fatal("expected error when gradient strip requested but source is not wallpaper")
	}
}

func TestPreparePushColors_GradientSkipsNonWallpaperDaemonMode(t *testing.T) {
	src := source.FromFlag("accent")
	tr := &transport.WLEDTransport{IP: "127.0.0.1"}
	_, skip, err := daemon.PreparePushColors(src, tr, 1.0, daemon.PushOptions{WallpaperGradientStrip: true}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !skip {
		t.Fatal("expected skip when daemon-mode and source is not wallpaper")
	}
}

func TestDeliverPreparedColors_NoHTTPWhenDedupeSolid(t *testing.T) {
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	orig := wled.TestStatePostURL
	wled.TestStatePostURL = srv.URL + "/json/state"
	t.Cleanup(func() { wled.TestStatePostURL = orig })

	rgb := [3]uint8{10, 20, 30}
	tr := &daemon.DedupeTracker{}
	tr.MarkSolidSent(rgb)

	wtr := &transport.WLEDTransport{IP: "ignored"}
	err := daemon.DeliverPreparedColors(wtr, 255, 1.0, tr, daemon.PreparedColors{Solid: rgb}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if posts != 0 {
		t.Fatalf("dedupe should skip POST, got %d", posts)
	}
}

func TestDeliverPreparedColors_PostsWhenSolidChanged(t *testing.T) {
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	orig := wled.TestStatePostURL
	wled.TestStatePostURL = srv.URL + "/json/state"
	t.Cleanup(func() { wled.TestStatePostURL = orig })

	tr := &daemon.DedupeTracker{}
	wtr := &transport.WLEDTransport{IP: "ignored"}
	err := daemon.DeliverPreparedColors(wtr, 255, 1.0, tr, daemon.PreparedColors{Solid: [3]uint8{1, 2, 3}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if posts != 1 {
		t.Fatalf("want 1 POST, got %d", posts)
	}
}
