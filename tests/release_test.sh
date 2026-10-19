#!/usr/bin/env bash
set -euo pipefail
umask 077
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -r -- "$work"' EXIT
mkdir -p "$work/tools" "$work/internal/update" "$work/dist"
cp "$root/tools/sign-release" "$work/tools/"
openssl genpkey -algorithm ED25519 -out "$work/key.pem"
openssl pkey -in "$work/key.pem" -pubout -out "$work/internal/update/release.pub"
printf 'invented package\n' > "$work/dist/omihomo-1.0.0-1-x86_64.pkg.tar.zst"
printf 'invented helper\n' > "$work/dist/omihomo-update-1.0.0-x86_64"
export OMIHOMO_SIGNING_KEY
OMIHOMO_SIGNING_KEY=$(cat "$work/key.pem")
"$work/tools/sign-release" 1.0.0 >"$work/output" 2>&1
[[ ! -s $work/output && $(stat -c %s "$work/dist/omihomo-release.txt.sig") == 64 ]]
openssl pkeyutl -verify -rawin -pubin -inkey "$work/internal/update/release.pub" \
  -in "$work/dist/omihomo-release.txt" -sigfile "$work/dist/omihomo-release.txt.sig" >/dev/null
[[ $(sed -n '1p' "$work/dist/omihomo-release.txt") == OMIHOMO-RELEASE-1 \
  && $(sed -n '2p' "$work/dist/omihomo-release.txt") == version=1.0.0 \
  && $(wc -l < "$work/dist/omihomo-release.txt") == 8 ]]
cp "$work/dist/omihomo-release.txt.sig" "$work/first.sig"
"$work/tools/sign-release" 1.0.0 >/dev/null
cmp "$work/first.sig" "$work/dist/omihomo-release.txt.sig"
for version in 01.0.0 1.0.0-rc1 ../1.0.0 9999999999.0.0; do
  if "$work/tools/sign-release" "$version" >"$work/output" 2>&1; then
    printf 'FAIL signing accepted invalid version %s\n' "$version" >&2
    exit 1
  fi
done
openssl genpkey -algorithm ED25519 -out "$work/wrong.pem"
OMIHOMO_SIGNING_KEY=$(cat "$work/wrong.pem")
if "$work/tools/sign-release" 1.0.0 >"$work/output" 2>&1; then printf 'FAIL signing accepted a wrong key\n' >&2; exit 1; fi
OMIHOMO_SIGNING_KEY=$(cat "$work/key.pem")
truncate -s 134217729 "$work/dist/omihomo-1.0.0-1-x86_64.pkg.tar.zst"
if "$work/tools/sign-release" 1.0.0 >"$work/output" 2>&1; then printf 'FAIL signing accepted an oversized package\n' >&2; exit 1; fi
unset OMIHOMO_SIGNING_KEY
printf 'PASS release signing, key matching, canonical versions, size limits and deterministic signatures\n'

sed -n '/^\[tasks.release\]/,$p' "$root/mise.toml" |
  sed -n "/^run = '''$/,/^'''$/p" | sed '1d;$d' > "$work/release"
git init --quiet --bare "$work/remote"
git init --quiet --initial-branch=next "$work/checkout"
cd "$work/checkout"
git config user.name 'Release Fixture'
git config user.email 'release@example.org'
printf '{"version":"1.0.0"}\n' > manifest.json
git add manifest.json
git commit --quiet -m 'initial fixture'
git remote add origin "$work/remote"
git push --quiet origin HEAD:refs/heads/next HEAD:refs/heads/release
start=$(git rev-parse HEAD)
git tag -a v1.0.1 -m 'existing local tag'
local_tag=$(git rev-parse refs/tags/v1.0.1)
if usage_version=1.0.1 bash "$work/release" >"$work/output" 2>&1; then
  printf 'FAIL release accepted an existing local tag\n' >&2
  exit 1
fi
if ! actual_tag=$(git rev-parse --verify refs/tags/v1.0.1 2>/dev/null); then
  printf 'FAIL release deleted a pre-existing local tag\n' >&2
  exit 1
fi
[[ $actual_tag == "$local_tag" && $(git rev-parse HEAD) == "$start" \
  && -z $(git status --porcelain) ]]
git tag -d v1.0.1 >/dev/null
printf '#!/bin/sh\nexit 1\n' > "$work/remote/hooks/pre-receive"
chmod +x "$work/remote/hooks/pre-receive"
if usage_version=1.0.1 bash "$work/release" >"$work/output" 2>&1; then
  printf 'FAIL release ignored a rejected push\n' >&2
  exit 1
fi
[[ $(git rev-parse HEAD) == "$start" && $(jq -r .version manifest.json) == 1.0.0 \
  && -z $(git status --porcelain) ]]
if git show-ref --verify --quiet refs/tags/v1.0.1; then
  printf 'FAIL failed push left its new tag behind\n' >&2
  exit 1
fi
rm "$work/remote/hooks/pre-receive"
usage_version=1.0.1 bash "$work/release" >"$work/output" 2>&1
[[ $(git rev-parse HEAD) == "$(git --git-dir="$work/remote" rev-parse refs/heads/next)" \
  && $(git rev-parse refs/tags/v1.0.1) == "$(git --git-dir="$work/remote" rev-parse refs/tags/v1.0.1)" \
  && $(jq -r .version manifest.json) == 1.0.1 && -z $(git status --porcelain) ]]
printf 'PASS release local-tag refusal, failed-push rollback and atomic publication to a fixture remote\n'
