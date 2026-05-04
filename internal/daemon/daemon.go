// Package daemon wires Omarchy color sources to WLED pushes: dedupe, watch, and poll fallback.
package daemon

import (
	"fmt"
	"os"
	"time"

	"github.com/ericdahl-dev/omarchy-wled/internal/color"
	"github.com/ericdahl-dev/omarchy-wled/internal/source"
	"github.com/ericdahl-dev/omarchy-wled/internal/wallpaper"
	"github.com/ericdahl-dev/omarchy-wled/internal/wled"
	"github.com/fsnotify/fsnotify"
)

// PushOptions carries behavior beyond “what color”: spatial gradient strip length, etc.
type PushOptions struct {
	WallpaperGradientStrip bool
	GradientLEDCountOrZero int // 0 → query WLED /json/info
}

type gradientStripSnapshot struct {
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

// DedupeTracker skips HTTP when the last push already matched (solid or full strip).
type DedupeTracker struct {
	lastSolid    *[3]uint8
	lastGradient *gradientStripSnapshot
}

func (t *DedupeTracker) ShouldSendSolid(rgb [3]uint8) bool {
	if t.lastGradient != nil {
		return true
	}
	if t.lastSolid == nil {
		return true
	}
	return *t.lastSolid != rgb
}

func (t *DedupeTracker) MarkSolidSent(rgb [3]uint8) {
	c := rgb
	t.lastSolid = &c
	t.lastGradient = nil
}

func (t *DedupeTracker) ShouldSendGradient(colors [][3]uint8) bool {
	if t.lastSolid != nil {
		return true
	}
	if t.lastGradient == nil {
		return true
	}
	return !gradientSlicesEqual(t.lastGradient.colors, colors)
}

func (t *DedupeTracker) MarkGradientSent(colors [][3]uint8) {
	t.lastGradient = &gradientStripSnapshot{colors: dupGradientColors(colors)}
	t.lastSolid = nil
}

// PushCurrentColorIfChanged reads Omarchy, scales saturation, posts to WLED if different from last push.
func PushCurrentColorIfChanged(src source.Source, tracker *DedupeTracker, wledIP string, brightness int, saturation float64, opts PushOptions) {
	if opts.WallpaperGradientStrip {
		if _, ok := src.(*source.WallpaperAverage); !ok {
			return
		}
		n, err := wled.ResolveGradientLEDCount(wledIP, opts.GradientLEDCountOrZero)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return
		}
		stripRGB, err := wallpaper.ColumnStripForLEDCount(wallpaper.CurrentSymlink(), n)
		if err != nil {
			return
		}
		for i := range stripRGB {
			stripRGB[i] = color.ScaleSaturation(stripRGB[i], saturation)
		}
		if !tracker.ShouldSendGradient(stripRGB) {
			return
		}
		tracker.MarkGradientSent(stripRGB)
		if err := wled.PostSpatialGradientJSON(wledIP, stripRGB, brightness); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return
		}
		startRGB, endRGB := stripRGB[0], stripRGB[len(stripRGB)-1]
		fmt.Printf("Updated WLED → gradient strip rgb%v…%v bri=%d sat=%.2f leds=%d\n",
			startRGB, endRGB, brightness, saturation, n)
		return
	}

	rgb, err := src.Read()
	if err != nil {
		return
	}
	rgb = color.ScaleSaturation(rgb, saturation)
	if !tracker.ShouldSendSolid(rgb) {
		return
	}
	tracker.MarkSolidSent(rgb)
	if err := wled.PostSolidJSON(wledIP, rgb, brightness); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	fmt.Printf("Updated WLED → rgb%v bri=%d sat=%.2f\n", rgb, brightness, saturation)
}

// RunFsnotifyLoop watches src.WatchDir() and pushes after debounce when IsTrigger matches.
func RunFsnotifyLoop(wledIP string, brightness int, saturation float64, src source.Source, opts PushOptions) error {
	tracker := &DedupeTracker{}
	PushCurrentColorIfChanged(src, tracker, wledIP, brightness, saturation, opts)

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
				PushCurrentColorIfChanged(src, tracker, wledIP, brightness, saturation, opts)
			}
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "Watch error: %v\n", watchErr)
		}
	}
}

// PollTick runs when Sentinel() changes; used by the polling loop and tests.
func PollTick(wledIP string, brightness int, saturation float64, src source.Source, tracker *DedupeTracker, previousSentinel *string, opts PushOptions) {
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
	PushCurrentColorIfChanged(src, tracker, wledIP, brightness, saturation, opts)
}

// RunSentinelPollLoop is the fsnotify fallback: compare Sentinel() once per second.
func RunSentinelPollLoop(wledIP string, brightness int, saturation float64, src source.Source, opts PushOptions) {
	tracker := &DedupeTracker{}
	var previousSentinel string
	for {
		PollTick(wledIP, brightness, saturation, src, tracker, &previousSentinel, opts)
		time.Sleep(time.Second)
	}
}
