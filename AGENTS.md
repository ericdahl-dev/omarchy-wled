# omarchy-wled — Agent Instructions

## Project overview

Single-file Python tool that watches Omarchy theme/wallpaper changes and syncs color to a WLED LED device. See `CONTEXT.md` for domain glossary.

## Commands

```bash
# Run tests
python -m pytest test_omarchy_wled.py test_omarchy_wled_tui.py -q

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

- Go CLI/daemon uses `internal/*` packages (`source`, `daemon`, `wled`, `wallpaper`, ...); `main.go` is thin wiring
- Core logic lives in `omarchy_wled.py` — keep it single-file
- TUI lives in `omarchy_wled_tui.py` (Textual app) — intentional exception to the single-file rule
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
| `main.go` | Go CLI entry (flags, orchestration) |
| `internal/source` | `Source` interface; theme row vs wallpaper average |
| `internal/daemon` | Push dedupe, fsnotify watch, poll fallback |
| `internal/wallpaper` | Wallpaper decode, γ pipeline, column→LED strip |
| `internal/wled` | WLED HTTP (solid, spatial `seg.i`, LED count) |
| `main_test.go` | Go test suite |
| `omarchy_wled.py` | Python core (TUI / optional pip install) |
| `omarchy_wled_tui.py` | Textual TUI (`omarchy-wled-tui` entry point) |
| `test_omarchy_wled.py` | pytest suite (core) |
| `test_omarchy_wled_tui.py` | pytest suite (TUI) |
| `PKGBUILD` | AUR package |
| `pyproject.toml` | Python package metadata |
| `omarchy-wled@.service` | systemd user service template |
| `.github/workflows/aur-release.yml` | AUR release automation |
| `.github/workflows/ci.yml` | `go test` + pytest on push/PR |

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->
