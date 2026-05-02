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
    def watch_path(self) -> Path: ...
    def is_trigger(self, event_path: str) -> bool: ...
    def sentinel(self) -> object: ...


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
        return Path(event_path).resolve() == BACKGROUND_LINK.resolve()


def make_source(name: str) -> ThemeColorSource | BgColorSource:
    if name == "bg":
        return BgColorSource()
    return ThemeColorSource("foreground" if name == "fg" else "accent")


# ---------------------------------------------------------------------------
# Color reading
# ---------------------------------------------------------------------------

def _read_color_key(key: str, path: Path) -> tuple[int, int, int]:
    text = path.read_text()
    match = re.search(rf'^{key}\s*=\s*"#([0-9a-fA-F]{{6}})"', text, re.MULTILINE)
    if not match:
        raise ValueError(f"{key} color not found in {path}")
    hex_color = match.group(1)
    return int(hex_color[0:2], 16), int(hex_color[2:4], 16), int(hex_color[4:6], 16)


def read_bg_color(link: Path = BACKGROUND_LINK) -> tuple[int, int, int]:
    """Return perceptual average color of the current wallpaper image.

    Averages in linear light (gamma-decoded) then re-encodes to sRGB, so the
    result matches what the eye sees rather than being biased toward bright pixels.
    """
    try:
        from PIL import Image
    except ImportError:
        raise RuntimeError("Pillow not installed — run: pip install Pillow")
    img_path = link.resolve()
    with Image.open(img_path) as img:
        rgb = img.convert("RGB")

    # Decode sRGB → linear, average, re-encode → sRGB
    pixels = list(rgb.getdata())
    n = len(pixels)
    lin_r = sum((r / 255) ** 2.2 for r, g, b in pixels) / n
    lin_g = sum((g / 255) ** 2.2 for r, g, b in pixels) / n
    lin_b = sum((b / 255) ** 2.2 for r, g, b in pixels) / n
    return (
        round(lin_r ** (1 / 2.2) * 255),
        round(lin_g ** (1 / 2.2) * 255),
        round(lin_b ** (1 / 2.2) * 255),
    )


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

try:
    from watchdog.events import FileSystemEventHandler as _FSEHandler
except ImportError:
    _FSEHandler = object  # type: ignore


class WatchHandler(_FSEHandler):
    """Watchdog event handler that pushes color to WLED on source-triggered changes."""

    def __init__(self, source: ColorSource, state: dict, ip: str, brightness: int, saturation: float):
        if _FSEHandler is not object:
            super().__init__()
        self._source = source
        self._state = state
        self._ip = ip
        self._brightness = brightness
        self._saturation = saturation

    def _maybe_push(self, path: str) -> None:
        if not self._source.is_trigger(path):
            return
        time.sleep(0.2)
        try:
            push_if_changed(self._source, self._state, self._ip, self._brightness, self._saturation)
        except Exception as exc:
            print(f"Error: {exc}", file=sys.stderr)

    def on_modified(self, event):
        self._maybe_push(event.src_path)

    def on_created(self, event):
        self._maybe_push(event.src_path)

    def on_moved(self, event):
        self._maybe_push(event.dest_path)


def watch(
    ip: str,
    brightness: int = 255,
    saturation: float = 1.0,
    source: ColorSource = None,
) -> None:
    """Watch for theme/background changes and update WLED."""
    if source is None:
        source = ThemeColorSource()

    state = {}

    try:
        from watchdog.observers import Observer
    except ImportError:
        print("watchdog not installed — falling back to polling (1s interval)", file=sys.stderr)
        _poll(ip, brightness, saturation, source, state)
        return

    handler = WatchHandler(source=source, state=state, ip=ip, brightness=brightness, saturation=saturation)

    try:
        push_if_changed(source, state, ip, brightness, saturation)
    except Exception as exc:
        print(f"Error: {exc}", file=sys.stderr)

    observer = Observer()
    observer.schedule(handler, str(source.watch_path().parent), recursive=False)
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
    state: dict = None,
) -> None:
    if source is None:
        source = ThemeColorSource()
    if state is None:
        state = {}
    last_sentinel = None
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
