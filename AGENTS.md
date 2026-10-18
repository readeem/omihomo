# Omihomo

This project is an Omarchy 4 plugin for managing mihomo proxy client, with subscriptions, configs and traffic rules.

A root-owned daemon embeds mihomo and serves an owner-only socket; the `omihomo` CLI and the QML
widget are its clients. Vocabulary is in `GLOSSARY.md`, decisions in `docs/adr/`.

## What we never compromise on

1. **Owner-only control.** Only the recorded owner reaches the socket, and client input is typed
   and allowlisted: never a path, unit or environment value (ADR-0001, ADR-0006).
2. **Downloads cannot change listeners, paths or privileges.** The running config is built from
   typed settings plus an allowlisted subscription (ADR-0005).
3. **mihomo stays behind `core.Core`.** Nothing outside `internal/core/mihomo` imports it;
   `TestOnlyTheCoreImportsMihomo` enforces this.

## The ways to hurt yourself

1. **The installed daemon is the machine's live proxy.** Never run `omihomo claim`, `pacman -U`,
   `tools/test-cutover` or a restart of `omihomo.service`; hand those steps to us, with
   `tests/README.md#manual-cutover-test`.
2. **mihomo races with itself.** Its package runs without `-race`, and only the release build tags
   (`with_gvisor,no_easytier`) give it the gVisor stack its tests need.
3. **`release` is what users install.** It is the default branch, but pull requests target `next`;
   only the release workflow moves `release` (ADR-0007).

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
