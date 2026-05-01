import json
import textwrap
import omarchy_wled
from pathlib import Path
from unittest.mock import MagicMock
import pytest

from omarchy_wled import (
    apply_saturation,
    send_color_to_wled,
    push_if_changed,
    ThemeColorSource,
    BgColorSource,
    make_source,
    _read_color_key,
    read_bg_color,
)

# ---------------------------------------------------------------------------
# Fixtures / helpers
# ---------------------------------------------------------------------------

COLORS_TOML_VALID = textwrap.dedent("""\
    accent = "#82FB9C"
    foreground = "#ddf7ff"
    background = "#0B0C16"
""")

COLORS_TOML_MISSING = textwrap.dedent("""\
    foreground = "#ddf7ff"
    background = "#0B0C16"
""")


def make_colors_toml(tmp_path: Path, content: str) -> Path:
    p = tmp_path / "colors.toml"
    p.write_text(content)
    return p


def fake_opener(status: int = 200):
    """Return a callable that behaves like urllib.request.urlopen."""
    resp = MagicMock()
    resp.status = status
    resp.__enter__ = lambda s: s
    resp.__exit__ = MagicMock(return_value=False)
    captured = {}

    def opener(req, timeout=None):
        captured["req"] = req
        return resp

    opener.captured = captured
    return opener


# ---------------------------------------------------------------------------
# _read_color_key
# ---------------------------------------------------------------------------

def test_read_accent_color_parses_hex(tmp_path):
    p = make_colors_toml(tmp_path, COLORS_TOML_VALID)
    assert _read_color_key("accent", p) == (130, 251, 156)


def test_read_accent_color_raises_on_missing_key(tmp_path):
    p = make_colors_toml(tmp_path, COLORS_TOML_MISSING)
    with pytest.raises(ValueError, match="accent color not found"):
        _read_color_key("accent", p)


# ---------------------------------------------------------------------------
# read_bg_color
# ---------------------------------------------------------------------------

def test_read_bg_color_returns_average_rgb(tmp_path):
    from PIL import Image
    img = Image.new("RGB", (2, 2))
    img.putpixel((0, 0), (255, 0, 0))
    img.putpixel((1, 0), (255, 0, 0))
    img.putpixel((0, 1), (0, 0, 255))
    img.putpixel((1, 1), (0, 0, 255))
    img_path = tmp_path / "bg.png"
    img.save(img_path)

    symlink = tmp_path / "background"
    symlink.symlink_to(img_path)

    r, g, b = read_bg_color(symlink)
    assert g == 0
    assert 120 <= r <= 135
    assert 120 <= b <= 135


# ---------------------------------------------------------------------------
# apply_saturation
# ---------------------------------------------------------------------------

def test_apply_saturation_full_preserves_color():
    assert apply_saturation(130, 251, 156, 1.0) == (130, 251, 156)


def test_apply_saturation_zero_produces_greyscale():
    r, g, b = apply_saturation(130, 251, 156, 0.0)
    assert r == g == b


def test_apply_saturation_clamps_above_one():
    r, g, b = apply_saturation(100, 200, 150, 999.0)
    assert 0 <= r <= 255 and 0 <= g <= 255 and 0 <= b <= 255


def test_apply_saturation_boost_increases_vividness():
    import colorsys
    r1, g1, b1 = apply_saturation(150, 180, 160, 1.0)
    r2, g2, b2 = apply_saturation(150, 180, 160, 2.0)
    _, s1, _ = colorsys.rgb_to_hsv(r1/255, g1/255, b1/255)
    _, s2, _ = colorsys.rgb_to_hsv(r2/255, g2/255, b2/255)
    assert s2 >= s1


# ---------------------------------------------------------------------------
# send_color_to_wled (injectable opener)
# ---------------------------------------------------------------------------

def test_send_color_posts_correct_payload():
    op = fake_opener(200)
    send_color_to_wled("192.168.1.50", 130, 251, 156, brightness=200, opener=op)

    req = op.captured["req"]
    assert req.full_url == "http://192.168.1.50/json/state"
    assert req.method == "POST"
    body = json.loads(req.data)
    assert body["on"] is True
    assert body["bri"] == 200
    assert body["seg"][0]["col"][0] == [130, 251, 156]


def test_send_color_raises_on_error_status():
    op = fake_opener(500)
    with pytest.raises(RuntimeError, match="HTTP 500"):
        send_color_to_wled("192.168.1.50", 130, 251, 156, opener=op)


# ---------------------------------------------------------------------------
# push_if_changed
# ---------------------------------------------------------------------------

class FixedColorSource:
    def __init__(self, color, sentinel_val="v1"):
        self._color = color
        self._sentinel_val = sentinel_val

    def read(self):
        return self._color

    def sentinel(self):
        return self._sentinel_val

    def is_trigger(self, path):
        return True


def test_push_if_changed_sends_on_first_call():
    op = fake_opener(200)
    state = {}
    push_if_changed(FixedColorSource((100, 150, 200)), state, "10.0.0.1", 255, 1.0, opener=op)
    req = op.captured["req"]
    body = json.loads(req.data)
    assert body["seg"][0]["col"][0] == [100, 150, 200]


def test_push_if_changed_does_not_resend_same_color():
    sends = []

    def counting_opener(req, timeout=None):
        sends.append(req)
        resp = MagicMock()
        resp.status = 200
        resp.__enter__ = lambda s: s
        resp.__exit__ = MagicMock(return_value=False)
        return resp

    state = {}
    src = FixedColorSource((100, 150, 200))
    push_if_changed(src, state, "10.0.0.1", 255, 1.0, opener=counting_opener)
    push_if_changed(src, state, "10.0.0.1", 255, 1.0, opener=counting_opener)
    assert len(sends) == 1


def test_push_if_changed_applies_saturation():
    op = fake_opener(200)
    state = {}
    push_if_changed(FixedColorSource((100, 200, 150)), state, "10.0.0.1", 255, 0.0, opener=op)
    body = json.loads(op.captured["req"].data)
    r, g, b = body["seg"][0]["col"][0]
    assert r == g == b


# ---------------------------------------------------------------------------
# ColorSource.is_trigger
# ---------------------------------------------------------------------------

def test_accent_source_triggers_on_theme_name_file(tmp_path):
    src = ThemeColorSource()
    assert src._key == "accent"
    from omarchy_wled import THEME_NAME_FILE
    assert src.is_trigger(str(THEME_NAME_FILE))


def test_bg_source_triggers_on_background_link(tmp_path):
    src = BgColorSource()
    from omarchy_wled import BACKGROUND_LINK
    assert src.is_trigger(str(BACKGROUND_LINK))
    assert not src.is_trigger("/some/path/theme.name")


# ---------------------------------------------------------------------------
# _read_color_key / FgColorSource
# ---------------------------------------------------------------------------

COLORS_TOML_WITH_FG = textwrap.dedent("""\
    accent = "#82FB9C"
    foreground = "#ddf7ff"
    background = "#0B0C16"
""")


def test_read_foreground_color_parses_hex(tmp_path):
    p = make_colors_toml(tmp_path, COLORS_TOML_WITH_FG)
    assert _read_color_key("foreground", p) == (221, 247, 255)


def test_read_foreground_color_raises_on_missing_key(tmp_path):
    p = make_colors_toml(tmp_path, "accent = \"#82FB9C\"\n")
    with pytest.raises(ValueError, match="foreground color not found"):
        _read_color_key("foreground", p)


def test_fg_source_triggers_on_theme_name_file():
    src = ThemeColorSource("foreground")
    from omarchy_wled import THEME_NAME_FILE
    assert src.is_trigger(str(THEME_NAME_FILE))


def test_make_source_fg_returns_theme_source():
    src = make_source("fg")
    assert isinstance(src, ThemeColorSource)
    assert src._key == "foreground"


def test_make_source_accent_returns_theme_source():
    src = make_source("accent")
    assert isinstance(src, ThemeColorSource)
    assert src._key == "accent"


def test_make_source_bg_returns_bg_source():
    assert isinstance(make_source("bg"), BgColorSource)


# ---------------------------------------------------------------------------
# ThemeColorSource.read end-to-end
# ---------------------------------------------------------------------------

def test_theme_color_source_read_accent(tmp_path, monkeypatch):
    p = make_colors_toml(tmp_path, COLORS_TOML_VALID)
    monkeypatch.setattr(omarchy_wled, "COLORS_TOML", p)
    assert ThemeColorSource("accent").read() == (130, 251, 156)


def test_theme_color_source_read_foreground(tmp_path, monkeypatch):
    p = make_colors_toml(tmp_path, COLORS_TOML_VALID)
    monkeypatch.setattr(omarchy_wled, "COLORS_TOML", p)
    assert ThemeColorSource("foreground").read() == (221, 247, 255)


# ---------------------------------------------------------------------------
# _poll — state sharing
# ---------------------------------------------------------------------------

def test_poll_uses_provided_state_dict():
    """_poll must use a passed-in state dict, not create a new one."""
    from omarchy_wled import _poll

    sends = []

    def counting_opener(req, timeout=None):
        resp = MagicMock(spec=["status", "__enter__", "__exit__"])
        resp.status = 200
        resp.__enter__ = lambda s: s
        resp.__exit__ = MagicMock(return_value=False)
        sends.append(req)
        return resp

    # Pre-populate state as if a color was already pushed before the fallback.
    shared_state = {"last_color": (100, 150, 200)}
    src = FixedColorSource((100, 150, 200), sentinel_val="s1")

    # Patch send_color_to_wled to use our opener so we can count calls.
    original_send = omarchy_wled.send_color_to_wled

    def patched_send(ip, r, g, b, brightness, opener=None):
        return original_send(ip, r, g, b, brightness, opener=counting_opener)

    omarchy_wled.send_color_to_wled = patched_send

    original_sleep = omarchy_wled.time.sleep
    call_count = 0

    def fake_sleep(secs):
        nonlocal call_count
        call_count += 1
        if call_count > 2:
            raise KeyboardInterrupt

    omarchy_wled.time.sleep = fake_sleep

    try:
        _poll("10.0.0.1", 255, 1.0, src, shared_state)
    except KeyboardInterrupt:
        pass
    finally:
        omarchy_wled.time.sleep = original_sleep
        omarchy_wled.send_color_to_wled = original_send

    # Because shared_state was pre-populated with the color, _poll should skip the send.
    assert len(sends) == 0
