// Package source maps Omarchy files (theme colors vs wallpaper) onto a single RGB producer + watch targets.
package source

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/ericdahl-dev/omarchy-wled/internal/color"
	"github.com/ericdahl-dev/omarchy-wled/internal/paths"
	"github.com/ericdahl-dev/omarchy-wled/internal/wallpaper"
)

// Source provides an RGB value plus filesystem locations used for fsnotify and polling.
type Source interface {
	Read() ([3]uint8, error)
	WatchDir() string
	// IsTrigger reports whether a filesystem event path should trigger a refresh (same paths fsnotify watches).
	IsTrigger(path string) bool
	// Sentinel returns a string that changes when the underlying theme/wallpaper state changes.
	// The daemon’s poll fallback compares it once per second when fsnotify is unavailable.
	Sentinel() (string, error)
}

// ThemeEntry reads one named entry from the active Omarchy colors.toml.
type ThemeEntry struct {
	TomlKey string
}

// Read returns the theme color at paths.ColorsToml.
func (s *ThemeEntry) Read() ([3]uint8, error) {
	return color.NamedHexFromColorsToml(s.TomlKey, paths.ColorsToml)
}

// WatchDir is the directory containing theme.name (switching themes retriggers).
func (s *ThemeEntry) WatchDir() string {
	return filepath.Dir(paths.ThemeNameFile)
}

// IsTrigger is true for writes to theme.name or colors.toml.
func (s *ThemeEntry) IsTrigger(eventPath string) bool {
	absEvent, _ := filepath.Abs(eventPath)
	absThemeName, _ := filepath.Abs(paths.ThemeNameFile)
	if absEvent == absThemeName {
		return true
	}
	absColorsToml, _ := filepath.Abs(paths.ColorsToml)
	return absEvent == absColorsToml
}

// Sentinel fingerprints theme.name and colors.toml mtimes so polling catches either changing.
func (s *ThemeEntry) Sentinel() (string, error) {
	themeNameInfo, err := os.Stat(paths.ThemeNameFile)
	if err != nil {
		return "", err
	}
	fingerprint := strconv.FormatInt(themeNameInfo.ModTime().UnixNano(), 10)
	if colorsInfo, err := os.Stat(paths.ColorsToml); err == nil {
		fingerprint += ":" + strconv.FormatInt(colorsInfo.ModTime().UnixNano(), 10)
	}
	return fingerprint, nil
}

// WallpaperAverage averages the current wallpaper image behind Omarchy’s background symlink.
type WallpaperAverage struct{}

// Read samples the wallpaper image linked from Omarchy’s background symlink.
func (s *WallpaperAverage) Read() ([3]uint8, error) {
	return wallpaper.AverageSRGBFromFile(wallpaper.CurrentSymlink())
}

// WatchDir is the directory containing the background symlink.
func (s *WallpaperAverage) WatchDir() string {
	return filepath.Dir(wallpaper.CurrentSymlink())
}

// IsTrigger fires when the symlink path itself changes.
func (s *WallpaperAverage) IsTrigger(eventPath string) bool {
	absEvent, _ := filepath.Abs(eventPath)
	absBackgroundSymlink, _ := filepath.Abs(wallpaper.CurrentSymlink())
	return absEvent == absBackgroundSymlink
}

// Sentinel is the readlink target so a switch to another image retriggers even if mtimes are weird.
func (s *WallpaperAverage) Sentinel() (string, error) {
	wallpaperTargetPath, err := os.Readlink(wallpaper.CurrentSymlink())
	if err != nil {
		return "", err
	}
	return wallpaperTargetPath, nil
}

// FromFlag builds the concrete source for CLI/TUI names (accent, fg, foreground, bg).
func FromFlag(sourceName string) Source {
	switch sourceName {
	case "bg":
		return &WallpaperAverage{}
	case "fg", "foreground":
		return &ThemeEntry{TomlKey: "foreground"}
	default:
		return &ThemeEntry{TomlKey: "accent"}
	}
}
