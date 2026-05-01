# omarchy-wled

Sync your [Omarchy](https://omarchy.org) theme accent color or wallpaper average color to a [WLED](https://kno.wled.ge) device in real time.

## Install

```bash
pipx install .
# or
uv tool install .
```

## Usage

```bash
omarchy-wled <WLED_IP> [options]

Options:
  --source {accent,bg}   Color source: accent (default) or wallpaper average
  -b, --brightness 0-255 LED brightness (default 255)
  -s, --saturation 0.0-1.0  Color saturation (default 1.0)
  --once                 Send once and exit
```

### Examples

```bash
# Watch for theme changes, use accent color
omarchy-wled 192.168.1.50

# Use wallpaper average color at 80% brightness
omarchy-wled 192.168.1.50 --source bg -b 200

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
