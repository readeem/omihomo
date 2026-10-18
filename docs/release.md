# Release workflow

GitHub Releases distributes the `omihomo-bin` Arch package for Linux x86_64. The widget is
an Omarchy plugin installed from git, from the `release` branch, which only ever points at a
published tag ([ADR-0007](adr/0007-release-branch.md)). Development lands on `next`. Pacman owns
the daemon and updater binaries, systemd units, installation hooks and removal. AUR publication
is optional.

## What CI publishes

The tag must match `manifest.json`. The full CI gate runs Go checks, the reachable-vulnerability
scan, installer tests and isolated widget-controller tests. The package workflow builds the
daemon and a small standard-library-only updater, then builds and tests the Arch package in
a clean container. Publication uses those same tested files:

- `omihomo-bin-X.Y.Z-1-x86_64.pkg.tar.zst`: the installable Arch package;
- `omihomo-update-X.Y.Z-x86_64`: the same updater included in the package, for first-install bootstrap;
- `omihomo-X.Y.Z-x86_64.tar.gz`: the binaries, units and license notices;
- `omihomo-X.Y.Z-vendor.tar.gz`: dependency source for the GPL distribution;
- `.sha256` files for these artifacts;
- `omihomo-release.txt` and its raw Ed25519 signature `omihomo-release.txt.sig`.

The signed manifest binds the version, package revision, architecture, exact size and SHA-256
of both executable artifacts. It contains no executable shell, paths, URLs, commands or keys.
The signing job verifies that its private key matches the checked-in public key before use.

CI installs and removes the package only inside its disposable Arch container. This checks
files, enablement, version and service sandboxing; it does not test a running TUN proxy.

## Installation trust

The plugin's `bin/omihomo-install` checks public signed release metadata as the normal user.
The initial trust is the plugin checkout and its `internal/update/release.pub` public key.
The widget caches update checks for twelve hours and also offers an explicit refresh.

Install/update opens a terminal and requests normal sudo authentication. An environment-cleared
shell downloads and verifies the manifest and bootstrap updater in a root-owned `0700`
temporary directory. It rejects an updater older than the plugin or a package downgrade
before executing it. The updater verifies the manifest again, downloads the exact tagged
package, checks its size and hash, and asks pacman to confirm installation. It checks the
package's name, version and architecture before installing it. Existing settings and ownership
stay in place; only first installation runs claim and enables the user's settings sync units.

Signature verification is mandatory in this path, independent of pacman's local-file signature
policy. The manifest signature authenticates the package bytes; it is not a pacman keyring
entry or detached OpenPGP package signature. Plain checksums are for download verification.

Both download implementations allow only HTTPS GitHub and GitHub asset hosts, bound redirects,
sizes and timeouts, and ignore environment-configured proxies. Installed version comparisons
include the package revision. Equal or older packages are refused, including a recheck just
before installation. Pacman keeps its interactive confirmation, and network timeouts never
kill a running package transaction. Another package manager can still change installed state
concurrently; review pacman's transaction summary before accepting.

Update first runs `omarchy plugin update` for a checkout Omarchy manages. It fetches `release`
over HTTPS without a signature, shows the diff and asks before fast-forwarding; the new checkout
is trusted as the original one was. Its installer, key and minimum version then verify the package.

The root proxy daemon has no update operation and keeps its existing sandbox. The remaining
trust is the plugin checkout, release signing key, reviewed CI code, and signed package content:
a valid signed package can execute installation hooks as root. Signatures authenticate content;
GitHub can still withhold releases, so they do not prove that a newer release does not exist.

## Configure publication

1. Create a GitHub environment named `release` under **Settings → Environments**.
   Add a required reviewer and disable administrator bypass. For a sole maintainer,
   leave **Prevent self-review** unchecked.
2. Choose **Selected branches and tags**, then add a **Tag** rule matching `v*`.
   Add no branch rule. Do this before pushing a tag: a workflow referencing a missing
   environment creates it without protection rules.
3. Keep the signing private key in a private directory outside version control, with directory
   mode `0700` and file mode `0600`. Generate it once if a matching key does not already exist:

   ```sh
   umask 077
   openssl genpkey -algorithm ED25519 -out "$SIGNING_KEY"
   openssl pkey -in "$SIGNING_KEY" -pubout -out internal/update/release.pub
   ```

   Set `SIGNING_KEY` to an unused private filename first. Commit only the public key.
   Keep a secure backup; routine releases must use the same key.
4. From this checkout, store the matching private key through stdin:

   ```sh
   gh secret set OMIHOMO_SIGNING_KEY --env release < "$SIGNING_KEY"
   gh secret list --env release
   ```

   The secret belongs only to `release`. Never put its contents in command arguments,
   workflow files, logs, issues or repository variables. No personal access token is needed:
   the publication job requests `contents: write` for `GITHUB_TOKEN`.
5. Generate a dedicated deploy key at an unused private filename. The workflow uses it to move
   `release`, so it needs write access, and only the `release` environment holds it:

   ```sh
   umask 077
   ssh-keygen -t ed25519 -N '' -C 'Omihomo release branch' -f "$DEPLOY_KEY"
   gh repo deploy-key add "$DEPLOY_KEY.pub" --allow-write --title 'release branch'
   gh secret set OMIHOMO_RELEASE_DEPLOY_KEY --env release < "$DEPLOY_KEY"
   ```

   Delete the private file afterwards; a lost key is replaced, not recovered.
6. Rename `main` to `release` under **Settings → Branches**; GitHub makes it the default branch,
   retargets open pull requests and redirects the old name. Existing plugin checkouts follow,
   since `omarchy plugin update` fetches the remote's default branch.
7. Under **Settings → Rules → Rulesets**, give `release` a ruleset that restricts updates,
   creations and deletions and blocks force pushes, with **Deploy keys** as the only bypass.
   Pull requests target `next`; nobody pushes to `release` by hand.
8. Keep `AUR_PUBLISH` unset unless the optional AUR setup below is complete.

Environment reviewers and restrictions depend on the GitHub plan for private repositories;
they are available for public repositories on current plans. See
[GitHub's environment instructions](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments).

Changing the signing key requires a reviewed plugin update containing the replacement public
key and a newly built updater. Update that trusted checkout before using releases signed with
the new key. Never accept replacement keys from release metadata. Replace the environment
secret and retire the old private key; an old checkout or binary still contains its old trust root.

## Publish a version

Complete [manual cutover testing](../tests/README.md#manual-cutover-test) and a maintainer
security review before publication. Review the existing root service's networking and
process-inspection privileges, owner-only control socket, private subscription state, dependency
scan, and the new authenticated installer and signing-key exposure. The release publishes
binaries, dependency source, checksums and signed metadata. It adds no network listener or
new Go dependency; bootstrap uses Arch's curl and OpenSSL tools.

Then, from a clean `next` that matches `origin/next`:

```sh
mise run release 1.2.3
```

It sets `version` in `manifest.json` and commits the bump when it changes, tags `v1.2.3`, and
pushes the commit and the tag together. It refuses a version older than the manifest's, one
already tagged, and a `next` that does not descend from `release`, as CI does. After CI passes,
open the run's **Review deployments**, select `release`, and choose **Approve and deploy** following the
security review. CI signs metadata, uploads all assets to a draft, publishes only after upload
succeeds, then fast-forwards `release` to the tag. Installed widgets find the release at their
next check. AUR runs afterward only when enabled.

Published assets are immutable in this workflow: reruns compare bytes and fail on differences.
Arch's rolling build environment can change package build metadata; prefer rerunning only
failed jobs. Never replace a published package or signature with different bytes. Release
changed contents under a new version.

## Optional AUR publication

AUR is a second distribution channel for the same daemon package. It is not needed for
GitHub installs or widget update checks.

1. Obtain an AUR account with permission to push `omihomo-bin`. Check availability and
   resolve existing ownership before enabling publication. Registration availability is
   controlled by Arch; do not assume forum accounts also work on AUR.
2. Generate a dedicated unencrypted SSH key at an unused private filename:

   ```sh
   umask 077
   ssh-keygen -t ed25519 -N '' -C 'Omihomo AUR releases' -f "$AUR_KEY"
   cat "$AUR_KEY.pub"
   ```

   Add only the public line to **My Account → SSH Public Key**, preserving existing keys.
   This key grants access to that account's packages, not just this package.
3. Verify the server fingerprint against the [AUR home page](https://aur.archlinux.org/),
   then test authentication without publishing:

   ```sh
   ssh -i "$AUR_KEY" -o IdentitiesOnly=yes aur@aur.archlinux.org list-repos
   ```

4. Create an `aur` GitHub environment with the same tag-only `v*` restriction. Store its
   SSH private key as an environment secret through stdin:

   ```sh
   gh secret set AUR_SSH_PRIVATE_KEY --env aur < "$AUR_KEY"
   ```

5. Set repository variable `AUR_PUBLISH` to `true`. The first publication creates the
   package repository contents; no initial manual AUR push is needed.

The workflow pins AUR's Ed25519 host key. Verify any replacement through official Arch
infrastructure information before changing it. To rotate the account key, remove the old
public key on AUR as well as replacing the GitHub secret. See
[Arch's submission guidelines](https://wiki.archlinux.org/title/AUR_submission_guidelines).

## Local testing

`mise run package` writes packages and rendered AUR metadata to ignored `dist/`; it installs
nothing. Keep local builds separate from public releases. The private signing key is not
needed to build or test packages.
