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
printf 'invented package\n' > "$work/dist/omihomo-bin-1.0.0-1-x86_64.pkg.tar.zst"
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
truncate -s 134217729 "$work/dist/omihomo-bin-1.0.0-1-x86_64.pkg.tar.zst"
if "$work/tools/sign-release" 1.0.0 >"$work/output" 2>&1; then printf 'FAIL signing accepted an oversized package\n' >&2; exit 1; fi
unset OMIHOMO_SIGNING_KEY
printf 'PASS release signing, key matching, canonical versions, size limits and deterministic signatures\n'
