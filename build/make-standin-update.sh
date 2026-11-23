#!/usr/bin/env bash
# Makes a stand-in release of Matty Plugin to update to, for trying the plugin's own updates
# (Look for updates, server/update.go) on a development server, with nothing asked of GitHub.
# Usage: make-standin-update.sh <version> <folder>
#
# <folder> is a scratch folder outside this tree: made if it is not there, and otherwise empty or one
# this script made before (it holds keys/rsa.key). Into it go:
#   ceo.villa.matty-<version>-linux-amd64.tar.gz  the stand-in build (villanet_standin), made to say it
#                                                 is <version>, in the release layout, signed
#   rsa.pem, mldsa87.pem                          the two certificates it is signed with
#   keys/                                         the throwaway keys, made the first time and used
#                                                 again, so that every version in the folder has the
#                                                 same two certificates
# Then run the stand-in with the folder, `fakebb -releases <folder>`, which answers for it as GitHub
# answers for real releases; install the stand-in build (make standin-linux) on the development
# server, switch on System Console > Plugins > Matty > Updates > Look for updates, press Check now,
# then Install. The stand-in build asks only the stand-in, and checks the bundle against the
# certificates it serves before it hands it to the admin's browser.
#
# Nothing here is a release, or can pass for one: the keys are made here, a release build checks
# against the two certificates built into it, and the program is the stand-in build, which starts
# only where Mattermost's Site URL is this machine. build/sign-release.sh refuses to sign it.
# Needs: an openssl that signs ML-DSA-87 (3.5 or newer), tar with gzip, python3, sha256sum, stat,
# Go (GO=... to name it), and the panel already built (webapp/dist/main.js; make standin-linux
# builds it).
set -euo pipefail

die() { printf '%s\n' "$*" >&2; exit 1; }

[[ $# -eq 2 ]] || die "usage: make-standin-update.sh <version> <folder>"
version="$1"; folder="$2"
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO="${GO:-go}"
platform=linux-amd64
id=ceo.villa.matty

[[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || die "refusing: $version is not a version such as 4.9.0"
for tool in openssl tar python3 sha256sum stat; do
    command -v "$tool" >/dev/null || die "$tool not found"
done
command -v "$GO" >/dev/null || [[ -x "$GO" ]] || die "Go not found: set GO=/path/to/go"
openssl list -signature-algorithms 2>/dev/null | grep -q 'ML-DSA-87' || die "this openssl cannot sign ML-DSA-87"
[[ -f "$repo/webapp/dist/main.js" ]] || die "the panel is not built: make standin-linux builds it"

# Where it would be, links followed, before anything is made there. Never the tree, and never the
# folder of the real signing keys, which is known by its path alone and never looked into.
realpath_of() { python3 -I -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "$1"; }
folder="$(realpath_of "$folder")"
case "$folder/" in
    "$(cd "$repo" && pwd -P)/"*) die "refusing: $folder is inside this tree; give a scratch folder outside it" ;;
esac
signing="$(realpath_of "$HOME/.local/share/matty-signing")"
case "$folder/" in
    "$signing/"*) die "refusing: $folder is where the real signing keys are kept; give a scratch folder" ;;
esac
case "$signing/" in
    "$folder/"*) die "refusing: $folder holds the folder of the real signing keys; give a scratch folder" ;;
esac
# Empty, or made by this script before: it holds the throwaway key it made.
if [[ -e "$folder" ]]; then
    [[ -d "$folder" && ! -L "$folder" ]] || die "refusing: $folder is not a folder"
    if [[ -n "$(ls -A -- "$folder")" && ! -f "$folder/keys/rsa.key" ]]; then
        die "refusing: $folder is not empty and was not made by this script; give an empty scratch folder"
    fi
fi
mkdir -p -- "$folder"
bundle="$folder/$id-$version-$platform.tar.gz"
[[ ! -e "$bundle" ]] || die "refusing: $bundle is already there"

umask 077
tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT

# The throwaway keys and their certificates, made once for the folder.
keys="$folder/keys"
if [[ ! -f "$keys/rsa.key" || ! -f "$keys/mldsa87.key" ]]; then
    mkdir -p -- "$keys"
    openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:4096 -out "$keys/rsa.key" 2>/dev/null
    openssl genpkey -algorithm ML-DSA-87 -out "$keys/mldsa87.key" 2>/dev/null
    openssl req -x509 -new -key "$keys/rsa.key" -subj "/CN=Matty Plugin stand-in (RSA)" -days 3650 -out "$keys/rsa.pem"
    openssl req -x509 -new -key "$keys/mldsa87.key" -subj "/CN=Matty Plugin stand-in (ML-DSA-87)" -days 3650 -out "$keys/mldsa87.pem"
fi
cp -- "$keys/rsa.pem" "$folder/rsa.pem"
cp -- "$keys/mldsa87.pem" "$folder/mldsa87.pem"
chmod 644 "$folder/rsa.pem" "$folder/mldsa87.pem"

# The stand-in build, made to say it is <version>, so that once installed the card, /access and /me
# say that version.
root="$tmp/$id"
mkdir -p "$root/server/dist" "$root/webapp"
(cd "$repo" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$GO" build -trimpath -tags villanet_standin \
    -ldflags="-s -w -X main.standInVersion=$version" -o "$root/server/dist/plugin-$platform" ./server) \
    || die "the stand-in build did not build"
python3 -I -c '
import json, sys
m = json.load(open(sys.argv[1]))
m["version"] = sys.argv[2]
m["server"]["executables"] = {sys.argv[3]: "server/dist/plugin-" + sys.argv[3]}
json.dump(m, open(sys.argv[4], "w"), indent=4)
' "$repo/plugin.json" "$version" "$platform" "$root/plugin.json"
cp -r "$repo/assets" "$root/"
cp -r "$repo/webapp/dist" "$root/webapp/dist"
find "$root" -type d -exec chmod 755 {} +
find "$root" -type f -exec chmod 644 {} +
chmod 755 "$root/server/dist/plugin-$platform"

# The manifest and both signatures, as build/sign-release.sh makes them.
mapfile -t files < <(cd "$root" && find . -type f -printf '%P\n' | LC_ALL=C sort)
for p in "${files[@]}"; do
    [[ "$p" =~ ^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$ && ! "/$p/" =~ /\.\.?/ ]] || die "refusing path: $p"
done
mkdir "$root/release"
manifest="$root/release/manifest.txt"
{
    printf 'matty-release-manifest 1\nproduct %s\nkind plugin\nversion %s\nplatform %s\nfiles %d\n' \
        "$id" "$version" "$platform" "${#files[@]}"
    for p in "${files[@]}"; do
        printf '%s %d %s\n' "$(cd "$root" && sha256sum -- "$p" | cut -c1-64)" "$(cd "$root" && stat -c %s -- "$p")" "$p"
    done
} >"$manifest"
openssl pkeyutl -sign -inkey "$keys/rsa.key" \
    -digest sha512 -pkeyopt rsa_padding_mode:pss -pkeyopt rsa_pss_saltlen:64 -pkeyopt rsa_mgf1_md:sha512 \
    -in "$manifest" -out "$root/release/manifest.rsa.sig" || die "RSA signing failed"
openssl pkeyutl -sign -inkey "$keys/mldsa87.key" -in "$manifest" -out "$root/release/manifest.mldsa87.sig" || die "ML-DSA signing failed"
chmod 755 "$root/release"
chmod 644 "$root/release/"*

# Packed, and checked as the plugin will check it, before it is put in the folder.
tar -C "$tmp" -czf "$tmp/bundle.tar.gz" "$id"
(cd "$repo" && "$GO" build -o "$tmp/checkrelease" ./build/checkrelease) || die "the checker did not build"
"$tmp/checkrelease" -rsa-cert "$folder/rsa.pem" -mldsa-cert "$folder/mldsa87.pem" -version "$version" -platform "$platform" -exact "$tmp/bundle.tar.gz" >"$tmp/check.txt" \
    || { cat "$tmp/check.txt" >&2; die "the stand-in bundle did not pass the checker"; }
mv -- "$tmp/bundle.tar.gz" "$bundle"
chmod 644 "$bundle"

printf 'Made %s (stand-in %s %s, %d files), signed with the throwaway keys in %s\n' "$bundle" "$id" "$version" "${#files[@]}" "$keys"
printf 'Serve it: build/fakebb/fakebb -releases %q\n' "$folder"
