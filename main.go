// Command omarchy-wled syncs Omarchy accent/wallpaper color to a WLED device.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/ericdahl-dev/omarchy-wled/internal/config"
	"github.com/ericdahl-dev/omarchy-wled/internal/daemon"
	"github.com/ericdahl-dev/omarchy-wled/internal/paths"
	"github.com/ericdahl-dev/omarchy-wled/internal/source"
	"github.com/ericdahl-dev/omarchy-wled/internal/wallpaper"
)

// version is set at link time: go build -ldflags "-X main.version=v1.2.3"
var version = "dev"

// cliOpts holds parsed flags and positional args after parseArgs.
type cliOpts struct {
	sourceName string
	brightness int
	// saturationOrUnset is the -saturation flag value; -1 means “pick default from source.”
	saturationOrUnset float64
	once              bool
	showVersion       bool
	wledIP            string
	bgGradient        bool
	gradientLEDs      int
	gradientSample    string // average | row
	gradientRow       int    // 0–100 when row mode
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
	if v, ok := cfg["gradient"]; ok && config.ParseBool(v) {
		defaultGradient = true
	}
	defaultGradientLEDs := 0
	if v, ok := cfg["gradient_leds"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			defaultGradientLEDs = n
		}
	}
	defaultGradientSample := cfg["gradient_sample"]
	if defaultGradientSample == "" {
		defaultGradientSample = "average"
	}
	defaultGradientRow := 50
	if v, ok := cfg["gradient_row"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			defaultGradientRow = n
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
		"Wallpaper per-column strip (spatial fade via seg.i; requires -source bg; see -gradient-sample)")
	gradientLEDs := fs.Int("gradient-leds", defaultGradientLEDs,
		"LED count for spatial fade (0 = fetch from WLED /json/info)")
	gradientSample := fs.String("gradient-sample", defaultGradientSample,
		"Wallpaper gradient sampling: average (full-height column avg) or row (single scanline)")
	gradientRow := fs.Int("gradient-row", defaultGradientRow,
		"With gradient-sample=row: vertical position 0–100 (0=top, 100=bottom)")
	once := fs.Bool("once", false, "Send current color once and exit (no watching)")
	fs.Usage = func() {
		fmt.Fprintf(output, "Usage: omarchy-wled [options] [WLED_IP]\n")
		fmt.Fprintf(output, "   or: omarchy-wled tui\n\nOptions:\n")
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
	opts.gradientSample = *gradientSample
	opts.gradientRow = *gradientRow

	opts.wledIP = cfg["ip"]
	if fs.NArg() > 0 {
		opts.wledIP = fs.Arg(0)
	}
	return opts, nil
}

func validateCli(opts *cliOpts) error {
	if opts.bgGradient && opts.sourceName != "bg" {
		return fmt.Errorf("-gradient requires -source bg")
	}
	switch strings.TrimSpace(strings.ToLower(opts.gradientSample)) {
	case "", "average", "row":
	default:
		return fmt.Errorf("-gradient-sample must be average or row — got %q", opts.gradientSample)
	}
	if opts.gradientRow < 0 || opts.gradientRow > 100 {
		return fmt.Errorf("-gradient-row must be 0–100 — got %d", opts.gradientRow)
	}
	return nil
}

func main() {
	paths.Init()

	if len(os.Args) >= 2 && os.Args[1] == "tui" {
		if err := runTUI(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg := config.LoadFlatFile(paths.ConfigPath)
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
		fmt.Fprintln(os.Stderr, "Configure with: omarchy-wled tui")
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

	src := source.FromFlag(opts.sourceName)
	pushOpts := daemon.PushOptions{
		WallpaperGradientStrip: opts.bgGradient,
		GradientLEDCountOrZero: opts.gradientLEDs,
		GradientSample:         wallpaper.GradientSampleKindFromString(opts.gradientSample),
		GradientRowPercent:     opts.gradientRow,
	}

	if opts.once {
		tracker := &daemon.DedupeTracker{}
		daemon.PushCurrentColorIfChanged(src, tracker, opts.wledIP, opts.brightness, effectiveSaturation, pushOpts)
		return
	}

	if err := daemon.RunFsnotifyLoop(opts.wledIP, opts.brightness, effectiveSaturation, src, pushOpts); err != nil {
		fmt.Fprintf(os.Stderr, "Watch failed: %v — falling back to polling\n", err)
		daemon.RunSentinelPollLoop(opts.wledIP, opts.brightness, effectiveSaturation, src, pushOpts)
	}
}
