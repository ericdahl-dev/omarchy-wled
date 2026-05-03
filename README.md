# omarchy-wled

[![CI](https://github.com/ericdahl-dev/omarchy-wled/actions/workflows/ci.yml/badge.svg)](https://github.com/ericdahl-dev/omarchy-wled/actions/workflows/ci.yml)
[![AUR version](https://img.shields.io/aur/version/omarchy-wled)](https://aur.archlinux.org/packages/omarchy-wled)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Sync your [Omarchy](https://omarchy.org) theme accent color or wallpaper average color to a [WLED](https://kno.wled.ge) device in real time.

Written in Go — installs as a single static binary with **no runtime dependencies**.

## Install

```bash
# Via AUR (recommended — no Python or pip required)
yay -S omarchy-wled

# Pre-built binary (linux/amd64 or linux/arm64)
# Download the latest release from https://github.com/ericdahl-dev/omarchy-wled/releases
tar xzf omarchy-wled_v*.tar.gz
install -Dm755 omarchy-wled ~/.local/bin/omarchy-wled

# Or build from source (requires Go 1.21+)
go build -o omarchy-wled .
install -Dm755 omarchy-wled ~/.local/bin/omarchy-wled
```

## Usage

```bash
omarchy-wled [options] [WLED_IP]

Options:
  -source {accent,fg,foreground,bg}  Color source: accent (default), fg / foreground (colors.toml `foreground` = UI font color), or bg (wallpaper average)
  -brightness 0-255       LED brightness (default 255)
  -saturation SCALE       Saturation multiplier (0.0=greyscale, 1.0=unchanged, >1.0=boost; default 1.2 for accent, 1.0 for fg/bg)
  -gradient               With `-source bg`, sample left/right wallpaper halves and drive WLED’s Gradient effect (effect speed 0 for a static fade)
  -gradient-fx ID         WLED effect index for Gradient (stock firmware uses 46; override if yours differs)
  -once                   Send once and exit
  -v, -version            Print version and exit (release builds show tag, e.g. v1.2.3)
```

### Examples

```bash
# Watch for theme changes, use accent color
omarchy-wled 192.168.1.50

# Use foreground (font) color
omarchy-wled 192.168.1.50 -source fg

# Use wallpaper average color at 80% brightness
omarchy-wled 192.168.1.50 -source bg -brightness 200

# Horizontal wallpaper gradient (left/right halves → WLED Gradient effect)
omarchy-wled 192.168.1.50 -source bg -gradient

# Boost saturation (accent already defaults to 1.2)
omarchy-wled 192.168.1.50 -saturation 1.5

# Send once and exit
omarchy-wled 192.168.1.50 -once
```

## Configuration

Settings can be saved to `~/.config/omarchy-wled/config.toml` so you don't need
to pass flags every time (e.g. after setting up via `omarchy-wled-tui`):

```toml
ip = "192.168.1.50"
source = "accent"
brightness = 255
saturation = 1.2
# Optional (bg + gradient); gradient_fx matches WLED’s effect list (46 = Gradient on stock WLED)
# gradient = true
# gradient_fx = 46
```

## Auto-start (systemd)

```bash
cp omarchy-wled@.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now omarchy-wled@192.168.1.50
```

To pass extra flags (e.g. `-source bg -brightness 200`), edit the service file's `ExecStart` line.

## TUI configurator

A graphical configurator (`omarchy-wled-tui`) is available as a separate Python
tool in this repository. Install it with:

```bash
pip install ".[tui]"
# or
uv tool install ".[tui]"
```
