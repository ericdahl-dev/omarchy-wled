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
        "systemctl", "--user", "enable", "--now", "omarchy-wled@10.0.0.1"
    ]


def test_service_disable_runs_correct_command():
    runner = FakeRunner()
    svc = ServiceController("10.0.0.1", runner=runner)
    svc.disable()
    assert runner.calls[-1] == [
        "systemctl", "--user", "disable", "--now", "omarchy-wled@10.0.0.1"
    ]


def test_service_restart_runs_correct_command():
    runner = FakeRunner()
    svc = ServiceController("10.0.0.1", runner=runner)
    svc.restart()
    assert runner.calls[-1] == [
        "systemctl", "--user", "restart", "omarchy-wled@10.0.0.1"
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
