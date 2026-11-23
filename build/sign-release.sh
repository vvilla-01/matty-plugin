#!/usr/bin/env bash
# Signs a release of Matty Plugin with Matty's two keys, RSA 4096 and ML-DSA-87, checks the result
# with the plugin's own checker (build/checkrelease), and prints the verification info to publish.
# Usage: sign-release.sh [--unpinned] <key dir> <bundle.tar.gz>
#
# <key dir> is laid out as Matty's signing kit makes it (tools/release/make-keys.sh in the app):
# matty-rsa.p12, matty-mldsa87.p12, pass/matty-rsa.pass, pass/matty-mldsa87.pass. OpenSSL reads
# each key straight from its .p12 with -passin file:, so no password or key is ever on a command
# line, in the environment or in a file left behind.
#
# The bundle (make dist-linux) is replaced, under the same name, by the same files plus
# release/manifest.txt, release/manifest.rsa.sig and release/manifest.mldsa87.sig, and only once
# that new bundle has passed the checker, both against the keys' own certificates and against the
# two built into the plugin. A bundle that is already signed is refused, and so is the stand-in
# build, a plugin.yml or plugin.yaml (Mattermost reads either in place of plugin.json), and keys
# whose certificates are not the ones built in, unless --unpinned is given (for a test with
# throwaway keys: every plugin then reports the bundle as altered).
# Needs: an openssl that signs ML-DSA-87 (3.5 or newer), tar with gzip, python3, sha256sum, stat,
# and Go (GO=... to name it).
set -euo pipefail

die() { printf '%s\n' "$*" >&2; exit 1; }

unpinned=no
if [[ "${1:-}" == --unpinned ]]; then unpinned=yes; shift; fi
[[ $# -eq 2 ]] || die "usage: sign-release.sh [--unpinned] <key dir> <bundle.tar.gz>"
keys="$1"; bundle="$2"
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO="${GO:-go}"

RSA_KS="$keys/matty-rsa.p12"
PQC_KS="$keys/matty-mldsa87.p12"
RSA_PASS="$keys/pass/matty-rsa.pass"
PQC_PASS="$keys/pass/matty-mldsa87.pass"
for f in "$RSA_KS" "$PQC_KS" "$RSA_PASS" "$PQC_PASS" "$bundle"; do
    [[ -f "$f" ]] || die "missing $f"
done
[[ "$bundle" == *.tar.gz ]] || die "refusing: $bundle is not a .tar.gz bundle"
for tool in openssl tar python3 sha256sum stat; do
    command -v "$tool" >/dev/null || die "$tool not found"
done
command -v "$GO" >/dev/null || [[ -x "$GO" ]] || die "Go not found: set GO=/path/to/go"
openssl list -signature-algorithms 2>/dev/null | grep -q 'ML-DSA-87' || die "this openssl cannot sign ML-DSA-87"

umask 077
tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT

# The checker, built from this tree: the same code the plugin checks itself with.
(cd "$repo" && "$GO" build -o "$tmp/checkrelease" ./build/checkrelease) || die "the checker did not build"

# Unpack, refusing anything but plain files and folders under one top folder.
listing="$(tar -tzf "$bundle")" || die "$bundle does not read as a bundle"
while IFS= read -r member; do
    [[ "$member" != /* && "/$member/" != */../* ]] || die "refusing: $bundle holds $member"
done <<<"$listing"
# Before anything is written: only plain files (-) and folders (d), no links of either kind.
types="$(tar -tvzf "$bundle" | cut -c1 | sort -u | tr -d '\n')"
[[ "$types" =~ ^[-d]+$ ]] || die "refusing: $bundle holds something that is not a file or a folder"
mkdir "$tmp/unpacked"
# -p keeps each file's mode as packed, whatever the umask.
tar -xpzf "$bundle" -C "$tmp/unpacked" --no-same-owner
if [[ -n "$(find "$tmp/unpacked" \( -type l -o \( ! -type f ! -type d \) \) -print -quit)" ]]; then
    die "refusing: $bundle holds something that is not a file or a folder"
fi
mapfile -t tops < <(find "$tmp/unpacked" -mindepth 1 -maxdepth 1 -printf '%f\n')
[[ ${#tops[@]} -eq 1 && -d "$tmp/unpacked/${tops[0]}" ]] || die "refusing: $bundle does not hold exactly one folder"
top="${tops[0]}"
root="$tmp/unpacked/$top"
[[ -f "$root/plugin.json" ]] || die "refusing: $bundle has no plugin.json"
for name in plugin.yml plugin.yaml; do
    if [[ -e "$root/$name" || -L "$root/$name" ]]; then die "refusing: $bundle has a $name, which Mattermost reads before plugin.json"; fi
done
if [[ -e "$root/release" ]]; then die "refusing: $bundle is already signed (it has a release folder)"; fi

# What is signed for: the id, the version and the platforms plugin.json names.
read -r id version platforms < <(python3 -I -c '
import json, sys
m = json.load(open(sys.argv[1]))
print(m["id"], m["version"], ",".join(sorted(m.get("server", {}).get("executables", {}))) or "-")
' "$root/plugin.json") || die "plugin.json does not read"
[[ "$id" == ceo.villa.matty && "$top" == "$id" ]] || die "refusing: this is not a ceo.villa.matty bundle"
[[ "$version" != *+standin* ]] || die "refusing: version $version is the stand-in build, which is never signed"
[[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || die "refusing: version $version is not a release version"
[[ "$platforms" != - ]] || die "refusing: plugin.json names no server executables"

# The stand-in build is never released, whatever its version says: the sentence it refuses to start
# with (server/standin.go's standInSiteRefusal, the Makefile's NO_STANDIN guard) is in no release.
mapfile -t executables < <(python3 -I -c '
import json, sys
import posixpath
for p in sorted(set(json.load(open(sys.argv[1]))["server"]["executables"].values())):
    print(posixpath.normpath(p))
' "$root/plugin.json") || die "plugin.json does not read"
(( ${#executables[@]} > 0 )) || die "plugin.json's executables do not read"
for executable in "${executables[@]}"; do
    [[ "$executable" =~ ^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$ && ! "/$executable/" =~ /\.\.?/ ]] || die "refusing executable path: $executable"
    [[ -f "$root/$executable" ]] || die "refusing: plugin.json names $executable, which is not in $bundle"
    if grep -q -a "development build of Matty Plugin" -- "$root/$executable"; then
        die "refusing: $executable is the stand-in build, which is never signed"
    fi
done

# The manifest: every regular file but release/*, sorted by byte value.
mapfile -t files < <(cd "$root" && find . -type f ! -path './release/*' -printf '%P\n' | LC_ALL=C sort)
for p in "${files[@]}"; do
    [[ "$p" =~ ^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$ && ! "/$p/" =~ /\.\.?/ ]] || die "refusing path: $p"
done
mkdir "$root/release"
manifest="$root/release/manifest.txt"
{
    printf 'matty-release-manifest 1\nproduct %s\nkind plugin\nversion %s\nplatform %s\nfiles %d\n' \
        "$id" "$version" "$platforms" "${#files[@]}"
    for p in "${files[@]}"; do
        printf '%s %d %s\n' "$(cd "$root" && sha256sum -- "$p" | cut -c1-64)" "$(cd "$root" && stat -c %s -- "$p")" "$p"
    done
} >"$manifest"

# RSASSA-PSS with SHA-512, MGF1 with SHA-512 and a 64-byte salt; pure ML-DSA-87 with the empty
# context (the only one Android's own verifier takes; the manifest's first line separates it).
openssl pkeyutl -sign -inkey "$RSA_KS" -passin "file:$RSA_PASS" \
    -digest sha512 -pkeyopt rsa_padding_mode:pss -pkeyopt rsa_pss_saltlen:64 -pkeyopt rsa_mgf1_md:sha512 \
    -in "$manifest" -out "$root/release/manifest.rsa.sig" || die "RSA signing failed"
openssl pkeyutl -sign -inkey "$PQC_KS" -passin "file:$PQC_PASS" \
    -in "$manifest" -out "$root/release/manifest.mldsa87.sig" || die "ML-DSA signing failed"
[[ "$(stat -c %s "$root/release/manifest.rsa.sig")" -eq 512 ]] || die "the RSA signature is not 512 bytes"
[[ "$(stat -c %s "$root/release/manifest.mldsa87.sig")" -eq 4627 ]] || die "the ML-DSA signature is not 4627 bytes"
chmod 755 "$root/release"
chmod 644 "$root/release/"*

# The keystores' own certificates, which the signatures are checked against.
openssl pkcs12 -in "$RSA_KS" -passin "file:$RSA_PASS" -nokeys -clcerts 2>/dev/null \
    | openssl x509 -outform DER -out "$tmp/rsa.der" || die "the RSA certificate does not read"
openssl pkcs12 -in "$PQC_KS" -passin "file:$PQC_PASS" -nokeys -clcerts 2>/dev/null \
    | openssl x509 -outform DER -out "$tmp/mldsa87.der" || die "the ML-DSA certificate does not read"

# Repack as make dist-linux packs, and check the bundle that will be handed out, not the folder.
out="$(dirname -- "$bundle")/.$(basename -- "$bundle").signing"
if [[ -e "$out" ]]; then die "refusing: $out already exists"; fi
trap 'rm -rf -- "$tmp"; rm -f -- "$out"' EXIT
tar -C "$tmp/unpacked" -czf "$out" "$top"
"$tmp/checkrelease" -rsa-cert "$tmp/rsa.der" -mldsa-cert "$tmp/mldsa87.der" -version "$version" -exact "$out" >"$tmp/check.txt" \
    || { cat "$tmp/check.txt" >&2; die "the signed bundle did not pass the checker; $bundle is unchanged"; }

# Whether a plugin built from this tree, with the certificates built into it, will call it signed:
# if not, the bundle is left as it was, unless --unpinned asked for it anyway.
if "$tmp/checkrelease" -version "$version" -exact "$out" >"$tmp/pinned.txt"; then
    pinned="yes: these are the certificates built into the plugin"
elif [[ $unpinned == yes ]]; then
    pinned="NO: these are not the certificates built into the plugin, which will report this release as altered"
else
    die "refusing: these keys' certificates are not the two built into the plugin, which would report this release as altered; $bundle is unchanged"
fi

chmod --reference="$bundle" -- "$out"
mv -f -- "$out" "$bundle"
trap 'rm -rf -- "$tmp"' EXIT

fingerprint() { sha256sum -- "$1" | cut -c1-64 | tr 'a-f' 'A-F' | sed 's/../&:/g; s/:$//'; }
printf 'Signed %s (%s %s, %s, %d files)\n' "$bundle" "$id" "$version" "$platforms" "${#files[@]}"
printf 'Manifest  SHA-256 %s\n' "$(fingerprint "$manifest")"
printf 'RSA-4096  SHA-256 %s\n' "$(fingerprint "$tmp/rsa.der")"
printf 'ML-DSA-87 SHA-256 %s\n' "$(fingerprint "$tmp/mldsa87.der")"
printf 'Pinned:   %s\n' "$pinned"
