// Package paths holds XDG-style runtime paths under $HOME.
package paths

import (
	"os"
	"path/filepath"
)

// Runtime paths (set by Init from $HOME).
var (
	Home           string
	ColorsToml     string
	ThemeNameFile  string
	BackgroundLink string
	ConfigPath     string
)

// Init resolves Omarchy and app config paths. Call from main on startup.
func Init() {
	Home = os.Getenv("HOME")
	if Home == "" {
		Home = "/"
	}
	ColorsToml = filepath.Join(Home, ".config/omarchy/current/theme/colors.toml")
	ThemeNameFile = filepath.Join(Home, ".config/omarchy/current/theme.name")
	BackgroundLink = filepath.Join(Home, ".config/omarchy/current/background")
	ConfigPath = filepath.Join(Home, ".config/omarchy-wled/config.toml")
}
