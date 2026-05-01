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
from typing import Protocol

COLORS_TOML = Path.home() / ".config/omarchy/current/theme/colors.toml"
THEME_NAME_FILE = Path.home() / ".config/omarchy/current/theme.name"
BACKGROUND_LINK = Path.home() / ".config/omarchy/current/background"


# ---------------------------------------------------------------------------
# ColorSource protocol
# ---------------------------------------------------------------------------

class ColorSource(Protocol):
    def read(self) -> tuple[int, int, int]: ...
    def sentinel(self) -> object: ...
    def watch_path(self) -> Path: ...
    def is_trigger(self, event_path: str) -> bool: ...


class ThemeColorSource:
    """Color from the Omarchy theme (accent, foreground, etc.)."""

    def __init__(self, key: str = "accent"):
        self._key = key

    def read(self) -> tuple[int, int, int]:
        return _read_color_key(self._key, COLORS_TOML)

    def sentinel(self) -> object:
        return THEME_NAME_FILE.stat().st_mtime

    def watch_path(self) -> Path:
        return THEME_NAME_FILE

    def is_trigger(self, event_path: str) -> bool:
        return Path(event_path).resolve() == THEME_NAME_FILE.resolve()


class BgColorSource:
    """Color averaged from the current Omarchy wallpaper."""

    def read(self) -> tuple[int, int, int]:
        return read_bg_color()

    def sentinel(self) -> object:
        return str(BACKGROUND_LINK.readlink())

    def watch_path(self) -> Path:
        return BACKGROUND_LINK

    def is_trigger(self, event_path: str) -> bool:
        return Path(event_path).name == BACKGROUND_LINK.name


def make_source(name: str) -> ThemeColorSource | BgColorSource:
    if name == "bg":
        return BgColorSource()
    return ThemeColorSource("foreground" if name == "fg" else "accent")


# ---------------------------------------------------------------------------
# Color reading
# ---------------------------------------------------------------------------

def read_accent_color(path: Path = COLORS_TOML) -> tuple[int, int, int]:
    """Parse accent hex color from colors.toml, return (r, g, b)."""
    return _read_color_key("accent", path)


def read_foreground_color(path: Path = COLORS_TOML) -> tuple[int, int, int]:
    """Parse foreground hex color from colors.toml, return (r, g, b)."""
    return _read_color_key("foreground", path)


def _read_color_key(key: str, path: Path) -> tuple[int, int, int]:
    text = path.read_text()
    match = re.search(rf'^{key}\s*=\s*"#([0-9a-fA-F]{{6}})"', text, re.MULTILINE)
    if not match:
        raise ValueError(f"{key} color not found in {path}")
    hex_color = match.group(1)
    return int(hex_color[0:2], 16), int(hex_color[2:4], 16), int(hex_color[4:6], 16)


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


# ---------------------------------------------------------------------------
# Color transformation
# ---------------------------------------------------------------------------

def apply_saturation(r: int, g: int, b: int, saturation: float) -> tuple[int, int, int]:
    """Scale HSV saturation. 1.0=unchanged, 0.0=greyscale, >1.0=boost."""
    h, s, v = colorsys.rgb_to_hsv(r / 255, g / 255, b / 255)
    s = max(0.0, min(1.0, s * saturation))
    r2, g2, b2 = colorsys.hsv_to_rgb(h, s, v)
    return round(r2 * 255), round(g2 * 255), round(b2 * 255)


# ---------------------------------------------------------------------------
# WLED transport
# ---------------------------------------------------------------------------

def send_color_to_wled(
    ip: str,
    r: int,
    g: int,
    b: int,
    brightness: int = 255,
    opener=None,
) -> None:
    """Send solid color to WLED via JSON API. brightness 0-255."""
    if opener is None:
        opener = urllib.request.urlopen
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
    with opener(req, timeout=5) as resp:
        if resp.status not in (200, 207):
            raise RuntimeError(f"WLED returned HTTP {resp.status}")


# ---------------------------------------------------------------------------
# Push logic
# ---------------------------------------------------------------------------

def push_if_changed(
    source: ColorSource,
    state: dict,
    wled_ip: str,
    brightness: int,
    saturation: float,
    opener=None,
) -> None:
    """Read color from source, apply transforms, send to WLED if changed.

    state is a mutable dict with key 'last_color' persisted across calls.
    """
    color = source.read()
    color = apply_saturation(*color, saturation)
    if color == state.get("last_color"):
        return
    state["last_color"] = color
    send_color_to_wled(wled_ip, *color, brightness, opener=opener)
    print(f"Updated WLED → rgb{color} bri={brightness} sat={saturation:.2f}")


# ---------------------------------------------------------------------------
# Watching
# ---------------------------------------------------------------------------

def watch(
    ip: str,
    brightness: int = 255,
    saturation: float = 1.0,
    source: ColorSource = None,
) -> None:
    """Watch for theme/background changes and update WLED."""
    if source is None:
        source = ThemeColorSource()
    try:
        from watchdog.observers import Observer
        from watchdog.events import FileSystemEventHandler
    except ImportError:
        print("watchdog not installed — falling back to polling (1s interval)", file=sys.stderr)
        _poll(ip, brightness, saturation, source)
        return

    state = {}

    class Handler(FileSystemEventHandler):
        def _maybe_push(self, path: str) -> None:
            if not source.is_trigger(path):
                return
            time.sleep(0.2)
            try:
                push_if_changed(source, state, ip, brightness, saturation)
            except Exception as exc:
                print(f"Error: {exc}", file=sys.stderr)

        def on_modified(self, event):
            self._maybe_push(event.src_path)

        def on_created(self, event):
            self._maybe_push(event.src_path)

        def on_moved(self, event):
            self._maybe_push(event.dest_path)

    try:
        push_if_changed(source, state, ip, brightness, saturation)
    except Exception as exc:
        print(f"Error: {exc}", file=sys.stderr)

    observer = Observer()
    observer.schedule(Handler(), str(source.watch_path().parent), recursive=False)
    observer.start()
    print(f"Watching {source.watch_path().parent} for changes — Ctrl-C to stop")
    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        observer.stop()
    observer.join()


def _poll(
    ip: str,
    brightness: int = 255,
    saturation: float = 1.0,
    source: ColorSource = None,
) -> None:
    if source is None:
        source = ThemeColorSource()
    last_sentinel = None
    state = {}
    while True:
        try:
            current = source.sentinel()
            if current != last_sentinel:
                last_sentinel = current
                time.sleep(0.2)
                push_if_changed(source, state, ip, brightness, saturation)
        except Exception as exc:
            print(f"Error: {exc}", file=sys.stderr)
        time.sleep(1)


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main() -> None:
    parser = argparse.ArgumentParser(description="Sync Omarchy accent color to WLED")
    parser.add_argument("wled_ip", help="IP address or hostname of WLED device")
    parser.add_argument(
        "--source", choices=["accent", "fg", "bg"], default="accent",
        help="Color source: accent (theme accent color) or bg (wallpaper average, requires Pillow)"
    )
    parser.add_argument(
        "-s", "--saturation", type=float, default=None, metavar="SCALE",
        help="Saturation multiplier (0.0=greyscale, 1.0=unchanged, >1.0=boost, default 1.2 for accent, 1.0 otherwise)"
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

    if args.saturation is None:
        args.saturation = 1.2 if args.source == "accent" else 1.0

    source = make_source(args.source)

    if args.once:
        state = {}
        push_if_changed(source, state, args.wled_ip, args.brightness, args.saturation)
    else:
        watch(args.wled_ip, args.brightness, args.saturation, source)


if __name__ == "__main__":
    main()
