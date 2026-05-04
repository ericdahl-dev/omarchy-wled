package daemon

import (
	"fmt"
	"os"

	"github.com/ericdahl-dev/omarchy-wled/internal/color"
	"github.com/ericdahl-dev/omarchy-wled/internal/source"
	"github.com/ericdahl-dev/omarchy-wled/internal/wallpaper"
	"github.com/ericdahl-dev/omarchy-wled/internal/wled"
)

// PreparedColors is the saturated RGB payload after Read / wallpaper strip resolution (no HTTP, no dedupe).
type PreparedColors struct {
	IsGradient bool
	Solid      [3]uint8
	Gradient   [][3]uint8
	LEDCount   int
}

// PreparePushColors computes colors for the next WLED push.
//
// If opts.WallpaperGradientStrip is set but src is not wallpaper, behavior depends on errOnNonWallpaperGradient:
// daemon passes false (silent skip); TUI passes true (returns an error).
//
// If wallpaper column decode fails, strictStripDecode true surfaces the error (TUI); false skips silently (daemon).
//
// When skip is true, do not call DeliverPreparedColors — there is nothing to push (same as legacy early return).
func PreparePushColors(src source.Source, wledIP string, saturation float64, opts PushOptions, errOnNonWallpaperGradient, strictStripDecode bool) (prep PreparedColors, skip bool, err error) {
	if opts.WallpaperGradientStrip {
		if _, ok := src.(*source.WallpaperAverage); !ok {
			if errOnNonWallpaperGradient {
				return PreparedColors{}, false, fmt.Errorf("gradient requires wallpaper source")
			}
			return PreparedColors{}, true, nil
		}
		n, err := wled.ResolveGradientLEDCount(wledIP, opts.GradientLEDCountOrZero)
		if err != nil {
			return PreparedColors{}, false, err
		}
		stripRGB, err := wallpaper.ColumnStripForLEDCount(wallpaper.CurrentSymlink(), n)
		if err != nil {
			if strictStripDecode {
				return PreparedColors{}, false, err
			}
			return PreparedColors{}, true, nil
		}
		for i := range stripRGB {
			stripRGB[i] = color.ScaleSaturation(stripRGB[i], saturation)
		}
		prep.IsGradient = true
		prep.Gradient = stripRGB
		prep.LEDCount = n
		return prep, false, nil
	}

	rgb, err := src.Read()
	if err != nil {
		return PreparedColors{}, false, err
	}
	prep.Solid = color.ScaleSaturation(rgb, saturation)
	return prep, false, nil
}

// DeliverPreparedColors applies dedupe, POSTs to WLED, and optionally logs like the CLI daemon.
func DeliverPreparedColors(wledIP string, brightness int, saturation float64, tracker *DedupeTracker, prep PreparedColors, skip bool, logProgress bool) error {
	if skip {
		return nil
	}
	if prep.IsGradient {
		if len(prep.Gradient) == 0 {
			return nil
		}
		if !tracker.ShouldSendGradient(prep.Gradient) {
			return nil
		}
		tracker.MarkGradientSent(prep.Gradient)
		if err := wled.PostSpatialGradientJSON(wledIP, prep.Gradient, brightness); err != nil {
			return err
		}
		if logProgress {
			startRGB, endRGB := prep.Gradient[0], prep.Gradient[len(prep.Gradient)-1]
			fmt.Printf("Updated WLED → gradient strip rgb%v…%v bri=%d sat=%.2f leds=%d\n",
				startRGB, endRGB, brightness, saturation, prep.LEDCount)
		}
		return nil
	}
	if !tracker.ShouldSendSolid(prep.Solid) {
		return nil
	}
	tracker.MarkSolidSent(prep.Solid)
	if err := wled.PostSolidJSON(wledIP, prep.Solid, brightness); err != nil {
		return err
	}
	if logProgress {
		fmt.Printf("Updated WLED → rgb%v bri=%d sat=%.2f\n", prep.Solid, brightness, saturation)
	}
	return nil
}

// PushCurrentColorIfChanged reads Omarchy, scales saturation, posts to WLED if different from last push.
func PushCurrentColorIfChanged(src source.Source, tracker *DedupeTracker, wledIP string, brightness int, saturation float64, opts PushOptions) {
	prep, skip, err := PreparePushColors(src, wledIP, saturation, opts, false, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	if err := DeliverPreparedColors(wledIP, brightness, saturation, tracker, prep, skip, true); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
}
