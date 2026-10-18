# Embed mihomo and distribute the daemon as a signed Arch package

The daemon embeds a pinned mihomo Go library behind `core.Core`, so configuration, lifecycle and live data share an in-process boundary. Shipping a separate mihomo executable would retain the controller and privilege-grant machinery this architecture removes.

Release CI publishes the tested `omihomo-bin` package to GitHub Releases and signs metadata binding its bytes and the bootstrap updater to a checked-in public key. This keeps widget installation and update discovery available without an AUR account or a separate package repository. AUR is an optional second channel for the same package. Pacman owns installation and upgrades; the Omarchy plugin remains a separate git installation. The combined binary is GPL-3.0-only.

The proxy daemon never installs updates. A short-lived authenticated installer verifies downloads in root-owned private staging before pacman sees them, keeping package management outside the daemon's capabilities and network protocol. The plugin checkout and signing key remain trusted code and authority for root package installation.
