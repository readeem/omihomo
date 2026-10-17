# Embed mihomo and distribute the daemon through AUR

The daemon embeds a pinned mihomo Go library behind `core.Core`, so configuration, lifecycle and live data share an in-process boundary. Shipping a separate mihomo executable would retain the controller and privilege-grant machinery this architecture removes.

Release CI builds and tests the static daemon, publishes its archive to GitHub Releases, and updates the `omihomo-bin` AUR package with a real checksum. Pacman owns installation and upgrades; the Omarchy plugin remains a separate git installation. The combined binary is GPL-3.0-only.
