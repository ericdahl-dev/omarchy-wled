#!/usr/bin/env python3
"""Sync Omarchy accent or wallpaper average color to a WLED device."""

import argparse
import colorsys
import re
import sys
import time
import urllib.request
import urllib.error
import json
from pathlib import Path

COLORS_TOML = Path.home() / ".config/omarchy/current/theme/colors.toml"
THEME_NAME_FILE = Path.home() / ".config/omarchy/current/theme.name"
BACKGROUND_LINK = Path.home() / ".config/omarchy/current/background"


def read_accent_color(path: Path = COLORS_TOML) -> tuple[int, int, int]:
    """Parse accent hex color from colors.toml, return (r, g, b)."""
    text = path.read_text()
    match = re.search(r'^accent\s*=\s*"#([0-9a-fA-F]{6})"', text, re.MULTILINE)
    if not match:
        raise ValueError(f"accent color not found in {path}")
    hex_color = match.group(1)
    r = int(hex_color[0:2], 16)
    g = int(hex_color[2:4], 16)
    b = int(hex_color[4:6], 16)
    return r, g, b


def read_bg_color(link: Path = BACKGROUND_LINK) -> tuple[int, int, int]:
    """Return average color of the current wallpaper image."""
    try:
        from PIL import Image
    except ImportError:
        raise RuntimeError("Pillow not installed — run: pip install Pillow")
    img_path = link.resolve()
    with Image.open(img_path) as img:
        avg = img.convert("RGB").resize((1, 1), Image.LANCZOS).getpixel((0, 0))
    return avg


def read_color(source: str) -> tuple[int, int, int]:
    """Return color from 'accent' or 'bg' source."""
    if source == "bg":
        return read_bg_color()
    return read_accent_color()


def apply_saturation(r: int, g: int, b: int, saturation: float) -> tuple[int, int, int]:
    """Scale HSV saturation of an RGB color. saturation 0.0-1.0."""
    h, s, v = colorsys.rgb_to_hsv(r / 255, g / 255, b / 255)
    s = max(0.0, min(1.0, saturation))
    r2, g2, b2 = colorsys.hsv_to_rgb(h, s, v)
    return int(r2 * 255), int(g2 * 255), int(b2 * 255)


def send_color_to_wled(ip: str, r: int, g: int, b: int, brightness: int = 255) -> None:
    """Send solid color to WLED via JSON API. brightness 0-255."""
    url = f"http://{ip}/json/state"
    payload = json.dumps({
        "on": True,
        "bri": max(0, min(255, brightness)),
        "seg": [{"col": [[r, g, b]]}]
    }).encode()
    req = urllib.request.Request(
        url,
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=5) as resp:
        if resp.status not in (200, 207):
            raise RuntimeError(f"WLED returned HTTP {resp.status}")


def watch(ip: str, brightness: int = 255, saturation: float = 1.0, source: str = "accent") -> None:
    """Watch for theme/background changes and update WLED."""
    try:
        from watchdog.observers import Observer
        from watchdog.events import FileSystemEventHandler
    except ImportError:
        print("watchdog not installed — falling back to polling (1s interval)", file=sys.stderr)
        _poll(ip, brightness, saturation, source)
        return

    class Handler(FileSystemEventHandler):
        def __init__(self):
            self._last_color = None

        def _is_bg_event(self, path: str) -> bool:
            return source == "bg" and Path(path).name == BACKGROUND_LINK.name

        def on_modified(self, event):
            if Path(event.src_path).resolve() == THEME_NAME_FILE.resolve():
                time.sleep(0.2)
                self._push()
            elif self._is_bg_event(event.src_path):
                time.sleep(0.2)
                self._push()

        def on_created(self, event):
            if self._is_bg_event(event.src_path):
                time.sleep(0.2)
                self._push()

        def on_moved(self, event):
            if self._is_bg_event(event.dest_path):
                time.sleep(0.2)
                self._push()

        def _push(self):
            try:
                color = read_color(source)
                color = apply_saturation(*color, saturation)
                if color == self._last_color:
                    return
                self._last_color = color
                send_color_to_wled(ip, *color, brightness)
                print(f"Updated WLED → rgb{color} bri={brightness} sat={saturation:.2f}")
            except Exception as exc:
                print(f"Error: {exc}", file=sys.stderr)

    handler = Handler()
    handler._push()

    observer = Observer()
    observer.schedule(handler, str(THEME_NAME_FILE.parent), recursive=False)
    watch_label = "background + theme" if source == "bg" else "theme"
    observer.start()
    print(f"Watching {THEME_NAME_FILE.parent} for {watch_label} changes — Ctrl-C to stop")
    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        observer.stop()
    observer.join()


def _poll(ip: str, brightness: int = 255, saturation: float = 1.0, source: str = "accent") -> None:
    last_sentinel = None
    last_color = None
    while True:
        try:
            if source == "bg":
                sentinel = str(BACKGROUND_LINK.readlink())
            else:
                sentinel = THEME_NAME_FILE.stat().st_mtime
            if sentinel != last_sentinel:
                last_sentinel = sentinel
                time.sleep(0.2)
                color = read_color(source)
                if color != last_color:
                    color = apply_saturation(*color, saturation)
                    send_color_to_wled(ip, *color, brightness)
                    print(f"Updated WLED → rgb{color} bri={brightness} sat={saturation:.2f}")
                    last_color = color
        except Exception as exc:
            print(f"Error: {exc}", file=sys.stderr)
        time.sleep(1)


def main() -> None:
    parser = argparse.ArgumentParser(description="Sync Omarchy accent color to WLED")
    parser.add_argument("wled_ip", help="IP address or hostname of WLED device")
    parser.add_argument(
        "--source", choices=["accent", "bg"], default="accent",
        help="Color source: accent (theme accent color) or bg (wallpaper average, requires Pillow)"
    )
    parser.add_argument(
        "-s", "--saturation", type=float, default=1.0, metavar="0.0-1.0",
        help="Color saturation (0.0=greyscale, 1.0=full, default 1.0)"
    )
    parser.add_argument(
        "-b", "--brightness", type=int, default=255, metavar="0-255",
        help="LED brightness (0-255, default 255)"
    )
    parser.add_argument(
        "--once", action="store_true",
        help="Send current color once and exit (no watching)"
    )
    args = parser.parse_args()

    if args.once:
        color = read_color(args.source)
        color = apply_saturation(*color, args.saturation)
        send_color_to_wled(args.wled_ip, *color, args.brightness)
        print(f"Sent rgb{color} bri={args.brightness} sat={args.saturation:.2f} src={args.source} to {args.wled_ip}")
    else:
        watch(args.wled_ip, args.brightness, args.saturation, args.source)


if __name__ == "__main__":
    main()
