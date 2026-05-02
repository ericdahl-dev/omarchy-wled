"""Tests for omarchy_wled_tui — TDD suite."""
import pytest
from pathlib import Path

from omarchy_wled_tui import TuiConfig, load_config, save_config


# ---------------------------------------------------------------------------
# TuiConfig — model
# ---------------------------------------------------------------------------

def test_config_defaults():
    cfg = TuiConfig(ip="192.168.1.50")
    assert cfg.source == "accent"
    assert cfg.brightness == 255
    assert cfg.saturation == 1.2


def test_config_round_trip(tmp_path):
    path = tmp_path / "config.toml"
    cfg = TuiConfig(ip="10.0.0.1", source="fg", brightness=128, saturation=0.8)
    save_config(cfg, path)
    loaded = load_config(path)
    assert loaded == cfg


def test_load_config_missing_file_returns_none(tmp_path):
    assert load_config(tmp_path / "nonexistent.toml") is None


# ---------------------------------------------------------------------------
# TuiConfig — validation
# ---------------------------------------------------------------------------

def test_config_invalid_source_raises():
    with pytest.raises(ValueError, match="source"):
        TuiConfig(ip="10.0.0.1", source="invalid").validate()


def test_config_brightness_out_of_range_raises():
    with pytest.raises(ValueError, match="brightness"):
        TuiConfig(ip="10.0.0.1", brightness=300).validate()


def test_config_negative_saturation_raises():
    with pytest.raises(ValueError, match="saturation"):
        TuiConfig(ip="10.0.0.1", saturation=-0.1).validate()


def test_config_empty_ip_raises():
    with pytest.raises(ValueError, match="ip"):
        TuiConfig(ip="").validate()


def test_config_valid_does_not_raise():
    TuiConfig(ip="10.0.0.1", source="bg", brightness=0, saturation=0.0).validate()


def test_brightness_to_pct_full():
    assert TuiConfig(ip="x", brightness=255).brightness_pct == 100

def test_brightness_to_pct_half():
    assert TuiConfig(ip="x", brightness=128).brightness_pct == 50

def test_brightness_to_pct_zero():
    assert TuiConfig(ip="x", brightness=0).brightness_pct == 0

def test_brightness_from_pct_round_trip():
    cfg = TuiConfig.from_brightness_pct(ip="x", pct=75)
    assert cfg.brightness_pct == 75


# ---------------------------------------------------------------------------
# ServiceController
# ---------------------------------------------------------------------------

from omarchy_wled_tui import ServiceController


class FakeRunner:
    def __init__(self, returncode=0, stdout="", stderr=""):
        self.calls = []
        self._returncode = returncode
        self._stdout = stdout
        self._stderr = stderr

    def __call__(self, cmd, **kwargs):
        self.calls.append(cmd)
        result = type("R", (), {
            "returncode": self._returncode,
            "stdout": self._stdout,
            "stderr": self._stderr,
        })()
        return result


def test_service_enable_runs_correct_command():
    runner = FakeRunner()
    svc = ServiceController("10.0.0.1", runner=runner)
    svc.enable()
    assert runner.calls[-1] == [
        "systemctl", "--user", "enable", "--now", "omarchy-wled"
    ]


def test_service_disable_runs_correct_command():
    runner = FakeRunner()
    svc = ServiceController("10.0.0.1", runner=runner)
    svc.disable()
    assert runner.calls[-1] == [
        "systemctl", "--user", "disable", "--now", "omarchy-wled"
    ]


def test_service_restart_runs_correct_command():
    runner = FakeRunner()
    svc = ServiceController("10.0.0.1", runner=runner)
    svc.restart()
    assert runner.calls[-1] == [
        "systemctl", "--user", "restart", "omarchy-wled"
    ]


def test_service_is_active_returns_true_when_active():
    runner = FakeRunner(returncode=0)
    svc = ServiceController("10.0.0.1", runner=runner)
    assert svc.is_active() is True


def test_service_is_active_returns_false_when_inactive():
    runner = FakeRunner(returncode=3)
    svc = ServiceController("10.0.0.1", runner=runner)
    assert svc.is_active() is False


# ---------------------------------------------------------------------------
# TuiTheme — read Omarchy colors for TUI styling
# ---------------------------------------------------------------------------

import textwrap
from omarchy_wled_tui import TuiTheme

COLORS_TOML = textwrap.dedent("""\
    accent = "#82FB9C"
    foreground = "#ddf7ff"
    background = "#0B0C16"
""")


def test_tui_theme_reads_accent(tmp_path):
    p = tmp_path / "colors.toml"
    p.write_text(COLORS_TOML)
    theme = TuiTheme.from_toml(p)
    assert theme.accent == (130, 251, 156)


def test_tui_theme_reads_foreground(tmp_path):
    p = tmp_path / "colors.toml"
    p.write_text(COLORS_TOML)
    theme = TuiTheme.from_toml(p)
    assert theme.foreground == (221, 247, 255)


def test_tui_theme_reads_background(tmp_path):
    p = tmp_path / "colors.toml"
    p.write_text(COLORS_TOML)
    theme = TuiTheme.from_toml(p)
    assert theme.background == (11, 12, 22)


def test_tui_theme_accent_as_hex(tmp_path):
    p = tmp_path / "colors.toml"
    p.write_text(COLORS_TOML)
    theme = TuiTheme.from_toml(p)
    assert theme.accent_hex == "#82fb9c"

# ---------------------------------------------------------------------------
# _refresh_color_preview — injectable sender
# ---------------------------------------------------------------------------

from omarchy_wled_tui import OmarchyWledTui, TuiConfig


def test_refresh_color_preview_calls_sender_with_color(tmp_path, monkeypatch):
    import omarchy_wled
    colors = tmp_path / "colors.toml"
    colors.write_text('accent = "#82FB9C"\nforeground = "#ddf7ff"\nbackground = "#0B0C16"\n')
    monkeypatch.setattr(omarchy_wled, "COLORS_TOML", colors)

    sent = []
    cfg = TuiConfig(ip="10.0.0.1", source="accent", brightness=255, saturation=1.0)

    app = OmarchyWledTui.__new__(OmarchyWledTui)
    app._config = cfg
    app._sender = lambda ip, r, g, b, brightness: sent.append((ip, r, g, b, brightness))

    app._refresh_color_preview_with_sender(cfg, app._sender)

    assert len(sent) == 1
    ip, r, g, b, bri = sent[0]
    assert ip == "10.0.0.1"
    assert bri == 255


# ---------------------------------------------------------------------------
# WatchHandler — extracted from watch() closure
# ---------------------------------------------------------------------------

from omarchy_wled import WatchHandler


class AlwaysTriggerSource:
    def __init__(self, color=(100, 150, 200)):
        self._color = color
        self.read_count = 0

    def read(self):
        self.read_count += 1
        return self._color

    def sentinel(self):
        return "s1"

    def watch_path(self):
        from pathlib import Path
        return Path("/tmp/fake")

    def is_trigger(self, path):
        return True


def test_watch_handler_maybe_push_calls_push_if_changed(monkeypatch):
    import omarchy_wled
    sent = []

    def fake_push(source, state, ip, brightness, saturation, opener=None):
        sent.append((ip, brightness, saturation))

    monkeypatch.setattr(omarchy_wled, "push_if_changed", fake_push)
    monkeypatch.setattr(omarchy_wled.time, "sleep", lambda s: None)

    src = AlwaysTriggerSource()
    state = {}
    handler = WatchHandler(source=src, state=state, ip="10.0.0.1", brightness=255, saturation=1.0)
    handler._maybe_push("/tmp/fake")

    assert len(sent) == 1
    assert sent[0] == ("10.0.0.1", 255, 1.0)


def test_watch_handler_skips_non_trigger_path(monkeypatch):
    import omarchy_wled
    sent = []

    monkeypatch.setattr(omarchy_wled, "push_if_changed", lambda *a, **kw: sent.append(1))
    monkeypatch.setattr(omarchy_wled.time, "sleep", lambda s: None)

    class NeverTriggerSource(AlwaysTriggerSource):
        def is_trigger(self, path):
            return False

    handler = WatchHandler(source=NeverTriggerSource(), state={}, ip="10.0.0.1", brightness=255, saturation=1.0)
    handler._maybe_push("/tmp/other")
    assert len(sent) == 0
