# Release workflow

Tags publish the `omihomo-bin` package to GitHub Releases; the `release` branch, which Omarchy
installs the plugin from, follows them ([ADR-0007](adr/0007-release-branch.md)).

## Configure publication

1. Create a GitHub environment named `release` with a required reviewer and no administrator
   bypass; a sole maintainer leaves **Prevent self-review** unchecked. Restrict it to a **Tag**
   rule matching `v*` and no branch rule. Do this before pushing a tag: a workflow referencing a
   missing environment creates it unprotected.
2. Generate the signing key once, in a private directory outside version control, and commit
   only the public key. Keep a secure backup; every release uses the same key.

   ```sh
   umask 077
   openssl genpkey -algorithm ED25519 -out "$SIGNING_KEY"
   openssl pkey -in "$SIGNING_KEY" -pubout -out internal/update/release.pub
   gh secret set OMIHOMO_SIGNING_KEY --env release < "$SIGNING_KEY"
   ```

3. Generate the deploy key that moves `release`, and delete the private file afterwards; a lost
   key is replaced, not recovered.

   ```sh
   umask 077
   ssh-keygen -t ed25519 -N '' -C 'Omihomo release branch' -f "$DEPLOY_KEY"
   gh repo deploy-key add "$DEPLOY_KEY.pub" --allow-write --title 'release branch'
   gh secret set OMIHOMO_RELEASE_DEPLOY_KEY --env release < "$DEPLOY_KEY"
   ```

4. Give `release` a ruleset that restricts updates, creations and deletions and blocks force
   pushes, with **Deploy keys** as the only bypass. Pull requests target `next`.

Replacing the signing key needs a reviewed plugin update carrying the new public key and a newly
built updater, before any release signed with it. Never accept a key from release metadata. An
old checkout or binary keeps trusting the old key.

## Publish a version

Complete the [manual cutover test](../tests/README.md) and a security review first: the root
service's privileges, the owner-only socket, private subscription state, the dependency scan,
and the installer and signing key. Then, from a clean `next` that matches `origin/next`:

```sh
mise run release 1.2.3
```

After CI passes, approve the `release` deployment. CI signs, uploads to a draft, publishes, then
fast-forwards `release` to the tag. If CI fails before approval, nothing is published: fix it on
`next`, delete the tag locally and on `origin`, and run the task again.

Published assets are immutable: reruns compare bytes and fail on differences. Prefer rerunning
only failed jobs, since Arch's rolling build environment can change package metadata. Release
changed contents under a new version.

## Optional AUR publication

1. Get an AUR account that may push `omihomo-bin`.
2. Generate a dedicated SSH key and add only its public line under **My Account → SSH Public
   Key**. It grants access to all of that account's packages.

   ```sh
   umask 077
   ssh-keygen -t ed25519 -N '' -C 'Omihomo AUR releases' -f "$AUR_KEY"
   ssh -i "$AUR_KEY" -o IdentitiesOnly=yes aur@aur.archlinux.org list-repos
   ```

   Check the server fingerprint against the [AUR home page](https://aur.archlinux.org/) first.
3. Create an `aur` environment with the same `v*` tag rule, then store the key in it:

   ```sh
   gh secret set AUR_SSH_PRIVATE_KEY --env aur < "$AUR_KEY"
   ```

4. Set the repository variable `AUR_PUBLISH` to `true`. The first release creates the package.

The workflow pins AUR's host key; verify a replacement through official Arch sources. Rotating
the account key means removing the old public key on AUR as well as replacing the secret.
