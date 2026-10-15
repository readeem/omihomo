# Release workflow

Omihomo releases provide the Go daemon for Linux x86_64. The widget remains a separate Omarchy
plugin. The AUR package `omihomo-bin` installs the prebuilt daemon and its systemd units; it does
not compile Go on the user's machine.

## What CI builds

`.github/workflows/package.yml` is a reusable workflow called by CI and the release workflow.
It reads `manifest.json` and accepts versions of the form `X.Y.Z`. It builds a static binary
with `CGO_ENABLED=0`, `with_gvisor,no_easytier`, stripped symbols, and the version embedded in
the CLI. gVisor supports TUN without redirect; disabling EasyTier avoids its JIT under the
service's `MemoryDenyWriteExecute=yes` sandbox.

The `release` workflow artifact contains:

- `omihomo-X.Y.Z-x86_64.tar.gz`: a versioned directory with the binary, service unit, socket
  unit, tmpfiles rule, license, and the license notices of every vendored dependency in
  `THIRD_PARTY_LICENSES`, which the package installs under `/usr/share/licenses/omihomo-bin`;
- `omihomo-X.Y.Z-vendor.tar.gz`: the `go mod vendor` tree, so the release carries the source of
  every module linked into the binary, as GPLv3 requires;
- a `.sha256` checksum for each, in `sha256sum` format.

The archive uses sorted entries, the commit timestamp, fixed ownership, and gzip without a
stored timestamp. The package job renders `packaging/aur/PKGBUILD` with the actual version
and archive checksum, then generates `.SRCINFO`. The template's `0.0.0` and `SKIP` values are
placeholders; the rendered AUR files must contain the release version and a real checksum.

Inside a clean Arch Linux container, CI runs `makepkg` against that same archive, runs namcap,
installs the package, checks the CLI version, verifies the systemd units and their sandbox,
and checks removal disables the units. The container does not boot systemd, so this verifies
package installation and enablement, not a running TUN proxy. The `aur` artifact holds the
rendered `PKGBUILD`, `.SRCINFO`, and `omihomo.install` that will be published to AUR.

## Maintainer setup

Before the first release:

1. Set up an AUR account with permission to push `omihomo-bin` and register a dedicated SSH
   public key with that account.
2. In the GitHub repository, create an environment named `aur`. Under its deployment branch
   and tag rules, choose selected branches and tags and allow **tags only** matching `v*`.
   Do not allow ordinary branches to use this environment. The workflow additionally checks
   the exact numeric `vX.Y.Z` format. Required reviewers can be added if desired.
3. Add the corresponding private SSH key as the environment secret `AUR_SSH_PRIVATE_KEY`.
   Keep it in the `aur` environment, not as a repository-wide secret.
4. Ensure GitHub Actions can create releases with `contents: write`. Package-building jobs
   only need read access; only the AUR publication job uses the `aur` environment and SSH key.

The release job pins AUR's SSH ed25519 host key instead of trusting a key fetched at runtime:

```text
aur.archlinux.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEuBKrPzbawxA/k2g6NcyV5jmqwJ2s+zpgZGZ7tpLIcN
```

If AUR rotates that key, verify the replacement through AUR's official infrastructure
information before updating the workflow. Do not disable strict host key checking.

## Publish a version

Merge the intended changes and the `manifest.json` version bump first. From the exact commit
to release, create and push a tag matching that version. For example, for manifest version
`2.0.0`:

```sh
git tag v2.0.0
git push origin v2.0.0
```

These are maintainer instructions, not steps performed while implementing the workflow.
Pushing the tag starts `.github/workflows/release.yml`, which:

1. Rejects any tag outside `vX.Y.Z` or whose version differs from `manifest.json`.
2. Calls the reusable package workflow, including the Arch installation checks.
3. Uploads the tested tarball and its checksum to a draft GitHub release, then publishes it.
   On a rerun, it checks that already published assets match the tested bytes.
4. Uses the `aur` environment to push the rendered AUR metadata to the `omihomo-bin` AUR
   repository only after the GitHub release assets are available. It rejects publishing an
   older version over a newer AUR version.

Review both the GitHub release and AUR job result. A failed AUR publication can leave a valid
GitHub release without an updated AUR package; inspect the failed job before retrying. Never
replace a published archive with different bytes: AUR users verify the recorded checksum.
Make a new release for changed binary contents.

To verify a downloaded archive, put it and its `.sha256` file in the same directory and run:

```sh
sha256sum -c omihomo-2.0.0-x86_64.tar.gz.sha256
```

Users install and claim the daemon with the commands in [daemon installation](daemon.md#install-on-arch-linux).
Upgrades use the normal AUR update flow; the package restarts a running daemon. No standalone
core downloader or self-update service is added by this release path.
