# omarchy-wled

[![CI](https://github.com/ericdahl-dev/omarchy-wled/actions/workflows/ci.yml/badge.svg)](https://github.com/ericdahl-dev/omarchy-wled/actions/workflows/ci.yml)
[![AUR version](https://img.shields.io/aur/version/omarchy-wled)](https://aur.archlinux.org/packages/omarchy-wled)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Sync your [Omarchy](https://omarchy.org) theme accent color or wallpaper average color to a [WLED](https://kno.wled.ge) device in real time.

## Install

```bash
# Via AUR
yay -S omarchy-wled

# Or from source
pipx install .
# or
uv tool install .
```

## Usage

```bash
omarchy-wled <WLED_IP> [options]

Options:
  --source {accent,fg,bg}  Color source: accent (default), fg (foreground), or bg (wallpaper average)
  -b, --brightness 0-255   LED brightness (default 255)
  -s, --saturation SCALE   Saturation multiplier (0.0=greyscale, 1.0=unchanged, >1.0=boost; default 1.2 for accent, 1.0 for fg/bg)
  --once                   Send once and exit
```

### Examples

```bash
# Watch for theme changes, use accent color
omarchy-wled 192.168.1.50

# Use foreground (font) color
omarchy-wled 192.168.1.50 --source fg

# Use wallpaper average color at 80% brightness
omarchy-wled 192.168.1.50 --source bg -b 200

# Boost saturation (accent already defaults to 1.2)
omarchy-wled 192.168.1.50 -s 1.5

# Send once and exit
omarchy-wled 192.168.1.50 --once
```

## Auto-start (systemd)

```bash
cp omarchy-wled@.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now omarchy-wled@192.168.1.50
```

To pass extra flags (e.g. `--source bg -b 200`), edit the service file's `ExecStart` line.
