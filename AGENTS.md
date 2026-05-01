# omarchy-wled — Agent Instructions

## Project overview

Single-file Python tool that watches Omarchy theme/wallpaper changes and syncs color to a WLED LED device. See `CONTEXT.md` for domain glossary.

## Commands

```bash
# Run tests
python -m pytest test_omarchy_wled.py -q

# Install for dev (with all optional deps)
pip install -e ".[all]"

# Lint (none configured — match existing style)
```

## Release flow

1. Bump `pkgver` in `PKGBUILD` and `version` in `pyproject.toml`
2. Commit, push branch, open PR
3. Merge when CI green
4. `git tag vX.Y.Z && git push origin vX.Y.Z`
5. GitHub Action (`aur-release.yml`) auto-updates PKGBUILD checksums and pushes to AUR

## Architecture notes

- All logic lives in `omarchy_wled.py` — keep it single-file
- `ColorSource` protocol: `read()`, `watch_path()`, `is_trigger()` — the seam for new sources
- `sentinel()` is **not** on the protocol; it's a poll-fallback concern passed directly to `_poll()`
- `watchdog` and `Pillow` are optional deps — code catches `ImportError` at runtime
- Accent source defaults to 1.2× saturation; fg/bg default to 1.0×
- See `docs/adr/` for recorded decisions

## Open issues (as of 0.1.3)

All architectural issues from the initial audit have been resolved. No known open bugs.

## Key files

| File | Purpose |
|---|---|
| `omarchy_wled.py` | All logic |
| `test_omarchy_wled.py` | pytest suite |
| `PKGBUILD` | AUR package |
| `pyproject.toml` | Python package metadata |
| `omarchy-wled@.service` | systemd user service template |
| `.github/workflows/aur-release.yml` | AUR release automation |
| `.github/workflows/ci.yml` | pytest on push/PR |
