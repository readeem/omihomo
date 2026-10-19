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

- **Protocol and settings.** A change reaches the daemon, the CLI, the widget and `docs/daemon.md`.
- **Packaging.** A new file the daemon needs is installed by `tools/build-package` and
  `.github/workflows/package.yml`.

## Verifying

`mise tasks` lists the tasks. Run `mise run check` before handoff, and `mise run widget` when the
widget or installer changes. `mise run package` writes only to `dist/`.

See a widget change in `tools/screenshots/drive`: the real panel against a fake daemon, driven
with keys and clicks, with screenshots you can read. `drive start --visible` shows it to us too.
Stop the session when done.
