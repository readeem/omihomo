# Omihomo

This project is an Omarchy 4 plugin for managing mihomo proxy client, with subscriptions, configs and traffic rules.

A root-owned daemon embeds mihomo and serves an owner-only socket; the `omihomo` CLI and the QML
widget are its clients. Vocabulary is in `GLOSSARY.md`, decisions in `docs/adr/`.
Plugin and the daemon/CLI are updated together.

## What we never compromise on

1. **Owner-only control.** Only the recorded owner reaches the socket, and client input is typed
   and allowlisted: never a path, unit or environment value (ADR-0001, ADR-0006).
2. **Downloads cannot change listeners, paths or privileges.** The running config is built from
   typed settings plus an allowlisted subscription (ADR-0005).
3. **mihomo stays behind `core.Core`.** Nothing outside `internal/core/mihomo` imports it;
   `TestOnlyTheCoreImportsMihomo` enforces this.

## Hit every surface

- **Protocol and settings.** A change reaches the daemon, the CLI and the widget.
- **Packaging.** A new file the daemon needs is installed by `tools/build-package` and
  `.github/workflows/package.yml`.

## Verifying

`mise tasks` lists the tasks. Run `mise run check` before handoff, and `mise run widget` when the
widget or installer changes. `mise run package` writes only to `dist/`.

Users run stable Omarchy, while a development machine may run edge, so a widget change must load
on both: `mise run omarchy` loads the panel on the latest stable tag and the edge branch, and CI
runs it too. Use only shell APIs both channels have.

A change to the widget or the demo recaptures the README screenshots with
`tools/screenshots/capture`. If nothing visible changed, say so in a commit trailer instead,
`Screenshots: unchanged, <why>`; `tools/screenshots/fresh` enforces this on pull requests and
releases.

See a widget change in `tools/screenshots/drive`: the real panel against a fake daemon, driven
with keys and clicks, with screenshots you can read. `drive start --visible` shows it to us too.
Stop the session when done.
