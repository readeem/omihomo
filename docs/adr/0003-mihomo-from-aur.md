# The core comes from AUR, not from GitHub releases

mihoro and Koala Clash both download the mihomo binary themselves, which means owning download,
checksum verification, version pinning, and update logic. Omihomo is an Omarchy plugin, so Arch
is guaranteed, and `mihomo-bin` is current on AUR. We install with `yay` and let pacman own
versioning.

## Consequences

This deleted the single largest chunk of the CLI and is the main reason bash remained viable
(see ADR-0004). We lose Koala's stable/alpha core toggle, which is out of scope. Capabilities
granted per ADR-0002 are stripped when pacman replaces the binary, so an upgrade hook has to
re-grant them.
