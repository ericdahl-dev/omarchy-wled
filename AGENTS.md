# omarchy-wled — Agent Instructions

## Project overview

Go CLI/daemon that watches Omarchy theme/wallpaper changes and syncs color to a WLED device. Interactive setup via `omarchy-wled tui`. See `CONTEXT.md` for domain glossary.

## Commands

```bash
# Run tests
go test ./...

# Lint (none configured — match existing style)
go vet ./...
```

## Release flow

1. Bump `pkgver` in `PKGBUILD` (and `pkgrel` if needed)
2. Commit, push branch, open PR
3. Merge when CI green
4. `git tag vX.Y.Z && git push origin vX.Y.Z`
5. GitHub Action (`aur-release.yml`) auto-updates PKGBUILD checksums and pushes to AUR

## Architecture notes

- Core logic lives in Go (`main.go`, `wallpaper.go`, `wled.go`, `tui.go`, `tui_config.go`)
- `ColorSource` interface: `Read()`, `WatchDir()`, `IsTrigger()`, `Sentinel()`
- Poll fallback compares `Sentinel()` strings in `poll()` / `pollTick` when fsnotify is unavailable
- Accent source defaults to 1.2× saturation; fg/bg default to 1.0×
- See `docs/adr/` for recorded decisions

## Key files

| File | Purpose |
|---|---|
| `main.go` | Go CLI: Color Source, push/watch/poll, config |
| `wallpaper.go` | Go: wallpaper decode, γ pipeline, column→LED strip |
| `wled.go` | Go: WLED HTTP (solid, spatial `seg.i`, LED count) |
| `tui.go` | Bubble Tea TUI (`omarchy-wled tui`) |
| `tui_config.go` | TUI config load/save, systemd helpers, preview push |
| `main_test.go`, `tui_config_test.go` | Go tests |
| `PKGBUILD` | AUR package |
| `omarchy-wled@.service` | systemd user service template |
| `.github/workflows/aur-release.yml` | AUR release automation |
| `.github/workflows/ci.yml` | `go test` on push/PR |

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
