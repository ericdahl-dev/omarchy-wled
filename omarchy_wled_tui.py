"""Omarchy-style Textual TUI for omarchy-wled configuration."""
from __future__ import annotations

import subprocess
import tomllib
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

from omarchy_wled import COLORS_TOML, THEME_NAME_FILE, _read_color_key

CONFIG_PATH = Path.home() / ".config" / "omarchy-wled" / "config.toml"

# ---------------------------------------------------------------------------
# Config model
# ---------------------------------------------------------------------------

@dataclass
class TuiConfig:
    ip: str
    source: str = "accent"
    brightness: int = 255
    saturation: float = 1.2

    def validate(self) -> None:
        if not self.ip:
            raise ValueError("ip must not be empty")
        if self.source not in ("accent", "fg", "bg"):
            raise ValueError(f"source must be accent, fg, or bg — got {self.source!r}")
        if not (0 <= self.brightness <= 255):
            raise ValueError(f"brightness must be 0-255 — got {self.brightness}")
        if self.saturation < 0:
            raise ValueError(f"saturation must be >= 0 — got {self.saturation}")

    def __eq__(self, other):
        if not isinstance(other, TuiConfig):
            return NotImplemented
        return (
            self.ip == other.ip
            and self.source == other.source
            and self.brightness == other.brightness
            and abs(self.saturation - other.saturation) < 1e-9
        )

    @property
    def brightness_pct(self) -> int:
        """Brightness as 0-100 percentage for display."""
        return round(self.brightness / 255 * 100)

    @classmethod
    def from_brightness_pct(cls, ip: str, pct: int, **kwargs) -> "TuiConfig":
        """Construct with brightness given as 0-100 percentage."""
        brightness = round(max(0, min(100, pct)) / 100 * 255)
        return cls(ip=ip, brightness=brightness, **kwargs)


def save_config(cfg: TuiConfig, path: Path = CONFIG_PATH) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    content = (
        f'ip = "{cfg.ip}"\n'
        f'source = "{cfg.source}"\n'
        f'brightness = {cfg.brightness}\n'
        f'saturation = {cfg.saturation}\n'
    )
    path.write_text(content)


def load_config(path: Path = CONFIG_PATH) -> Optional[TuiConfig]:
    if not path.exists():
        return None
    data = tomllib.loads(path.read_text())
    return TuiConfig(
        ip=data["ip"],
        source=data.get("source", "accent"),
        brightness=int(data.get("brightness", 255)),
        saturation=float(data.get("saturation", 1.2)),
    )


# ---------------------------------------------------------------------------
# Service controller
# ---------------------------------------------------------------------------

class ServiceController:
    _SERVICE_TEMPLATE = """[Unit]\nDescription=Sync Omarchy theme color to WLED\nAfter=network.target graphical-session.target\nPartOf=graphical-session.target\n\n[Service]\nExecStart=%h/.local/bin/omarchy-wled\nRestart=on-failure\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n"""
    _SERVICE_DIR = Path.home() / ".config" / "systemd" / "user"
    _SERVICE_FILE = _SERVICE_DIR / "omarchy-wled@.service"

    def __init__(self, ip: str, runner=None):
        self._ip = ip
        self._run = runner or (lambda cmd, **kw: subprocess.run(cmd, **kw))

    def _install_service_file(self) -> None:
        self._SERVICE_DIR.mkdir(parents=True, exist_ok=True)
        self._SERVICE_FILE.write_text(self._SERVICE_TEMPLATE)
        self._run(["systemctl", "--user", "daemon-reload"])

    def _unit(self) -> str:
        return f"omarchy-wled@{self._ip}"

    def enable(self) -> None:
        self._install_service_file()
        self._run(["systemctl", "--user", "enable", "--now", self._unit()])

    def disable(self) -> None:
        self._run(["systemctl", "--user", "disable", "--now", self._unit()])

    def restart(self) -> None:
        self._run(["systemctl", "--user", "restart", self._unit()])

    def is_active(self) -> bool:
        result = self._run(
            ["systemctl", "--user", "is-active", "--quiet", self._unit()],
            capture_output=True,
        )
        return result.returncode == 0


# ---------------------------------------------------------------------------
# TuiTheme — Omarchy colors for TUI styling
# ---------------------------------------------------------------------------

@dataclass
class TuiTheme:
    accent: tuple[int, int, int]
    foreground: tuple[int, int, int]
    background: tuple[int, int, int]

    @classmethod
    def from_toml(cls, path: Path) -> "TuiTheme":
        return cls(
            accent=_read_color_key("accent", path),
            foreground=_read_color_key("foreground", path),
            background=_read_color_key("background", path),
        )

    @classmethod
    def fallback(cls) -> "TuiTheme":
        return cls(accent=(130, 251, 156), foreground=(221, 247, 255), background=(11, 12, 22))

    @property
    def accent_hex(self) -> str:
        return "#{:02x}{:02x}{:02x}".format(*self.accent)

    @property
    def foreground_hex(self) -> str:
        return "#{:02x}{:02x}{:02x}".format(*self.foreground)

    @property
    def background_hex(self) -> str:
        return "#{:02x}{:02x}{:02x}".format(*self.background)


# ---------------------------------------------------------------------------
# Textual TUI
# ---------------------------------------------------------------------------

from textual.app import App, ComposeResult
from textual.message import Message
from textual.binding import Binding
from textual.containers import Container, Vertical, Horizontal
from textual.css.query import NoMatches
from textual.reactive import reactive
from textual.widgets import (
    Button,
    Footer,
    Header,
    Input,
    Label,
    Select,
    Static,
    Switch,
)
from textual_slider import Slider
from textual import on, work


CSS = """
Screen {
    overflow-y: auto;
}

#title {
    text-align: center;
    padding: 1 2;
    color: $success;
    text-style: bold;
}

#setup-screen, #main-screen {
    padding: 1 2;
    height: auto;
}

.section-label {
    color: $success;
    text-style: bold;
    margin-top: 1;
}

.field-row {
    height: 3;
    margin-bottom: 1;
}

.field-label {
    width: 22;
    content-align: left middle;
}

.field-input {
    width: 1fr;
}

Slider {
    width: 1fr;
    height: 3;
}

Slider > .slider--slider {
    color: $success;
}

#color-preview {
    height: 3;
    margin: 1 0;
    content-align: center middle;
    text-style: bold;
}

#status-bar {
    height: 1;
    margin-top: 1;
    color: $success;
    text-align: center;
}

.error {
    color: $error;
}

.success {
    color: $success;
}

#btn-row {
    height: 3;
    margin-top: 1;
    align: center middle;
}

Button {
    margin: 0 1;
}
"""


def _load_theme() -> TuiTheme:
    try:
        return TuiTheme.from_toml(COLORS_TOML)
    except Exception:
        return TuiTheme.fallback()


class SetupScreen(Static):
    """Shown when no config exists — collects WLED IP."""

    DEFAULT_CSS = """
    SetupScreen {
        padding: 2 4;
    }
    #setup-title {
        text-align: center;
        text-style: bold;
        margin-bottom: 1;
    }
    #setup-hint {
        margin-bottom: 1;
        color: $foreground;
    }
    #setup-btn-row {
        height: 3;
        margin-top: 1;
        align: center middle;
    }
    """

    def compose(self) -> ComposeResult:
        yield Label("Welcome to omarchy-wled", id="setup-title")
        yield Label(
            "Enter the IP address or hostname of your WLED device to get started.",
            id="setup-hint",
        )
        yield Horizontal(
            Label("WLED IP / Host:", classes="field-label"),
            Input(placeholder="e.g. 192.168.1.50", id="setup-ip", classes="field-input"),
            classes="field-row",
        )
        yield Label("", id="setup-error", classes="error")
        yield Horizontal(
            Button("Continue", variant="primary", id="setup-continue"),
            id="setup-btn-row",
        )

    @on(Button.Pressed, "#setup-continue")
    def on_continue(self) -> None:
        ip = self.query_one("#setup-ip", Input).value.strip()
        if not ip:
            self.query_one("#setup-error", Label).update("IP address is required.")
            return
        self.post_message(SetupScreen.Done(ip))

    @on(Input.Submitted, "#setup-ip")
    def on_ip_submitted(self, _) -> None:
        self.on_continue()

    class Done(Message):
        def __init__(self, ip: str) -> None:
            super().__init__()
            self.ip = ip


class ColorPreview(Static):
    """A block that displays the current color being sent to WLED."""

    color: reactive[tuple[int, int, int]] = reactive((0, 0, 0))

    def render(self) -> str:
        r, g, b = self.color
        hex_color = f"#{r:02x}{g:02x}{b:02x}"
        return f"[on {hex_color}]   [/] [{hex_color}]  rgb({r}, {g}, {b})[/]"


class OmarchyWledTui(App):
    """Main TUI application."""

    TITLE = "omarchy-wled"
    CSS = CSS
    BINDINGS = [
        Binding("q", "quit", "Quit"),
        Binding("s", "save", "Save"),
    ]

    theme_colors: reactive[TuiTheme] = reactive(_load_theme, always_update=True)

    def __init__(self):
        super().__init__()
        self._config: Optional[TuiConfig] = load_config()
        self._theme_watcher = None
        self._debounce_timer = None

    def compose(self) -> ComposeResult:
        yield Header()
        yield Label("omarchy-wled  ·  WLED color sync", id="title")
        if self._config is None:
            yield SetupScreen(id="setup-screen")
        else:
            yield self._build_main()
        yield Label("", id="status-bar")
        yield Footer()

    def _build_main(self) -> Vertical:
        cfg = self._config
        theme = self.theme_colors

        return Vertical(
            Label("Configuration", classes="section-label"),
            Horizontal(
                Label("WLED IP / Host:", classes="field-label"),
                Input(value=cfg.ip, id="cfg-ip", classes="field-input"),
                classes="field-row",
            ),
            Horizontal(
                Label("Color source:", classes="field-label"),
                Select(
                    [("Accent", "accent"), ("Foreground", "fg"), ("Wallpaper avg", "bg")],
                    value=cfg.source,
                    id="cfg-source",
                    classes="field-input",
                ),
                classes="field-row",
            ),
            Horizontal(
                Label("Brightness (0-100%):", id="brightness-label", classes="field-label"),
                Slider(0, 100, step=2, value=cfg.brightness_pct, id="cfg-brightness", classes="field-input"),
                classes="field-row",
            ),
            Horizontal(
                Label(f"Saturation (0-200%):", id="saturation-label", classes="field-label"),
                Slider(0, 200, step=2, value=int(cfg.saturation * 100), id="cfg-saturation", classes="field-input"),
                classes="field-row",
            ),
            Label("Service", classes="section-label"),
            Horizontal(
                Label("Auto-start:", id="service-label", classes="field-label"),
                Switch(value=False, id="cfg-service"),
                classes="field-row",
            ),
            Label("Current color", classes="section-label"),
            ColorPreview(id="color-preview"),
            Horizontal(
                Button("Save", variant="primary", id="btn-save"),
                Button("Quit", id="btn-quit"),
                id="btn-row",
            ),
            id="main-screen",
        )

    def on_mount(self) -> None:
        self._start_theme_watcher()
        self._refresh_color_preview()
        self.call_after_refresh(self._update_service_switch)

    def _update_service_switch(self) -> None:
        try:
            cfg = self._current_config_from_ui()
            if cfg.ip:
                active = ServiceController(cfg.ip).is_active()
                self.query_one("#cfg-service", Switch).value = active
        except Exception:
            pass

    def _start_theme_watcher(self) -> None:
        self._last_theme_name: Optional[str] = None
        self.set_interval(2.0, self._check_theme)

    def _check_theme(self) -> None:
        try:
            name = THEME_NAME_FILE.read_text().strip()
        except OSError:
            return
        if name == self._last_theme_name:
            return
        self._last_theme_name = name
        new_theme = _load_theme()
        self.theme_colors = new_theme
        self._refresh_color_preview()

    def watch_theme_colors(self, theme: TuiTheme) -> None:
        self.app.dark = True
        try:
            self.app_stylesheet.variables.update({
                "accent": theme.accent_hex,
                "foreground": theme.foreground_hex,
                "background": theme.background_hex,
            })
            self.refresh_css()
        except Exception:
            pass

    def _refresh_color_preview_with_sender(self, cfg: TuiConfig, sender) -> None:
        from omarchy_wled import make_source, apply_saturation
        source = make_source(cfg.source)
        color = source.read()
        color = apply_saturation(*color, cfg.saturation)
        try:
            self.query_one(ColorPreview).color = color
        except Exception:
            pass
        if cfg.ip:
            sender(cfg.ip, *color, cfg.brightness)

    def _refresh_color_preview(self) -> None:
        try:
            from omarchy_wled import send_color_to_wled
            cfg = self._current_config_from_ui()
            self._refresh_color_preview_with_sender(cfg, send_color_to_wled)
        except Exception:
            pass

    def _current_config_from_ui(self) -> TuiConfig:
        try:
            ip = self.query_one("#cfg-ip", Input).value.strip()
            source = self.query_one("#cfg-source", Select).value or "accent"
            pct = self.query_one("#cfg-brightness", Slider).value
            saturation = self.query_one("#cfg-saturation", Slider).value / 100
            return TuiConfig.from_brightness_pct(ip=ip, pct=int(pct), source=str(source), saturation=saturation)
        except (NoMatches, ValueError):
            return self._config or TuiConfig(ip="")

    @on(SetupScreen.Done)
    def on_setup_done(self, message: SetupScreen.Done) -> None:
        self._config = TuiConfig(ip=message.ip)
        save_config(self._config)
        self.query_one("#setup-screen").remove()
        self.query_one("#status-bar", Label).update("")
        self.mount(self._build_main(), before="#status-bar")
        self._refresh_color_preview()

    def _schedule_refresh(self) -> None:
        """Debounce: cancel pending refresh and schedule a new one 300ms out."""
        if self._debounce_timer is not None:
            self._debounce_timer.stop()
        self._debounce_timer = self.set_timer(0.08, self._refresh_color_preview)

    @on(Slider.Changed, "#cfg-brightness")
    def on_brightness_changed(self, event: Slider.Changed) -> None:
        try:
            self.query_one("#brightness-label", Label).update(f"Brightness: {event.value}%")
        except NoMatches:
            pass
        self._schedule_refresh()

    @on(Slider.Changed, "#cfg-saturation")
    def on_saturation_changed(self, event: Slider.Changed) -> None:
        try:
            self.query_one("#saturation-label", Label).update(f"Saturation: {event.value / 100:.2f}\u00d7")
        except NoMatches:
            pass
        self._schedule_refresh()

    @on(Select.Changed, "#cfg-source")
    def on_source_changed(self, _) -> None:
        self._refresh_color_preview()

    @on(Switch.Changed, "#cfg-service")
    def on_service_toggle(self, event: Switch.Changed) -> None:
        cfg = self._current_config_from_ui()
        if not cfg.ip:
            self._set_status("IP is required to control service.", error=True)
            return
        try:
            cfg.validate()
        except ValueError as exc:
            self._set_status(str(exc), error=True)
            return
        save_config(cfg)
        self._config = cfg
        svc = ServiceController(cfg.ip)
        try:
            if event.value:
                svc.enable()
                self._set_status(f"Settings saved — service started: omarchy-wled@{cfg.ip}")
            else:
                svc.disable()
                self._set_status(f"Service stopped and disabled: omarchy-wled@{cfg.ip}")
        except Exception as exc:
            self._set_status(f"Service error: {exc}", error=True)

    @on(Button.Pressed, "#btn-save")
    def on_save_pressed(self) -> None:
        self.action_save()

    @on(Button.Pressed, "#btn-quit")
    def on_quit_pressed(self) -> None:
        self.exit()

    def action_save(self) -> None:
        cfg = self._current_config_from_ui()
        try:
            cfg.validate()
        except ValueError as exc:
            self._set_status(str(exc), error=True)
            return
        was_active = ServiceController(cfg.ip).is_active()
        save_config(cfg)
        self._config = cfg
        if was_active:
            try:
                ServiceController(cfg.ip).restart()
                self._set_status("Saved and service restarted.")
            except Exception as exc:
                self._set_status(f"Saved (service restart failed: {exc})", error=True)
        else:
            self._set_status("Configuration saved.")
        self._refresh_color_preview()

    def _set_status(self, msg: str, error: bool = False) -> None:
        bar = self.query_one("#status-bar", Label)
        bar.remove_class("error", "success")
        bar.add_class("error" if error else "success")
        bar.update(msg)


def main() -> None:
    OmarchyWledTui().run()


if __name__ == "__main__":
    main()
