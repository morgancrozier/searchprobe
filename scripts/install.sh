#!/bin/sh
# SearchProbe installer: no sudo, shell profile edits, telemetry, or binary execution.
set -eu
umask 077
fail() { printf 'SearchProbe: %s\n' "$*" >&2; exit 1; }
usage() {
    cat <<'EOF'
Install/update SearchProbe (macOS/Linux, arm64/amd64):
  sh install.sh --version vMAJOR.MINOR.PATCH[-prerelease]
  sh install.sh --from-dir /path/to/release-assets
  sh install.sh --version vMAJOR.MINOR.PATCH[-prerelease] [--base-url https://host/releases/download]
  sh install.sh --uninstall

Local bundles contain VERSION, checksums.txt and platform archives. HTTPS mode
fetches BASE_URL/VERSION/ASSET; GitHub downloads require a public release.
Only published versions can be downloaded. --dry-run reports selection without downloading
or changing files. Updates rerun the same command with the new bundle/version.
Installs only to $HOME/.local/share/searchprobe/bin; never edits shell profiles.
EOF
}
version='' from_dir='' base_url=https://github.com/morgancrozier/searchprobe/releases/download
uninstall=false dry_run=false custom_base=false
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version|--from-dir|--base-url)
            if [ "$#" -lt 2 ] || [ -z "$2" ]; then fail "Missing value for $1"; fi
            case "$1" in
                --version) version=$2 ;;
                --from-dir) from_dir=$2 ;;
                --base-url) base_url=$2; custom_base=true ;;
            esac
            shift 2 ;;
        --uninstall) uninstall=true; shift ;;
        --dry-run) dry_run=true; shift ;;
        --help|-h) usage; exit 0 ;;
        *) fail "Unknown argument: $1 (use --help)" ;;
    esac
done
[ -n "${HOME:-}" ] || fail 'HOME must be set.'
case "$HOME" in /*) ;; *) fail 'HOME must be an absolute path.' ;; esac
# Reject control characters and shell metacharacters that would make printed
# copy/paste PATH instructions unsafe. Spaces and ordinary Unicode are fine.
case "$HOME" in *\'*|*\"*|*\`*|*\$*|*\\*) fail 'HOME contains unsupported shell metacharacters.' ;; esac
[ "$(printf '%s' "$HOME" | tr -d '[:cntrl:]')" = "$HOME" ] || fail 'HOME contains control characters.'
[ "$(id -u)" != 0 ] || fail 'Run as your normal user, without sudo.'
install_dir=$HOME/.local/share/searchprobe/bin
binary=$install_dir/gsc
receipt=$install_dir/.searchprobe-sha256
if [ "$uninstall" = true ]; then
    if [ -n "$from_dir$version" ] || [ "$custom_base" != false ]; then fail '--uninstall cannot select a release.'; fi
else
    [ -z "$from_dir" ] || [ "$custom_base" = false ] || fail 'Use --from-dir or --base-url, not both.'
    if [ -n "$from_dir" ]; then
        from_dir=$(cd "$from_dir" && pwd -P) || fail 'Bundle directory does not exist.'
        [ -n "$version" ] || version=$(cat "$from_dir/VERSION") || fail 'Bundle needs VERSION or --version.'
    fi
    printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9]+([.-][a-zA-Z0-9]+)*)?$' || fail 'Specify --version vMAJOR.MINOR.PATCH (optional prerelease suffix).'
    case "$base_url" in https://?*) ;; *) fail 'Base URL must use HTTPS.' ;; esac
    case "$base_url" in *\?*|*\#*|*@*|*[[:space:]]*) fail 'Base URL must not contain credentials, queries, fragments, or whitespace.' ;; esac
    case "$(uname -s)" in Darwin) system=darwin ;; Linux) system=linux ;; *) fail 'Supported systems: macOS and Linux only.' ;; esac
    case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) fail 'Supported architectures: arm64 and amd64 only.' ;; esac
    asset=searchprobe_${version#v}_${system}_${arch}.tar.gz
    printf 'SearchProbe %s: %s -> %s\n' "$version" "$asset" "$binary"
fi
# Do not follow symlinks in our managed location (including intermediate dirs).
for dir in "$HOME/.local" "$HOME/.local/share" "$HOME/.local/share/searchprobe" "$install_dir"; do
    [ ! -L "$dir" ] || fail "Refusing symlink installation directory: $dir"
    [ ! -e "$dir" ] || [ -d "$dir" ] || fail "Not a directory: $dir"
    if [ -d "$dir" ]; then
        unsafe=$(find "$dir" -prune \( ! -user "$(id -u)" -o -perm -002 -o -perm -020 \) -print)
        [ -z "$unsafe" ] || fail "Installation directories must be owned by you and not group/world writable: $dir"
    fi
done
existing=$(command -v gsc 2>/dev/null || true)
if [ -n "$existing" ] && [ "$existing" != "$binary" ]; then
    printf 'PATH collision: gsc currently resolves to %s (possibly Ghostscript). It will not be touched.\n' "$existing"
fi
[ "$dry_run" = false ] || { printf 'Dry run; no files changed. Destination: %s\n' "$binary"; exit 0; }
if command -v sha256sum >/dev/null 2>&1; then
    hash() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
    hash() { shasum -a 256 "$1" | awk '{print $1}'; }
else
    fail 'Install sha256sum or shasum to verify downloads.'
fi
if [ "$uninstall" = true ] && [ ! -e "$install_dir" ]; then
    printf 'SearchProbe is not installed here. Credentials were not changed.\n'; exit 0
fi
mkdir -p "$install_dir"
lock=$HOME/.local/share/searchprobe/.install-lock
mkdir "$lock" 2>/dev/null || fail "Another install may be active. If interrupted, inspect and remove $lock before retrying."
tmp=
cleanup() { [ -z "$tmp" ] || rm -rf "$tmp"; rmdir "$lock"; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
# A matching receipt proves this installer owns the exact bytes being replaced.
if [ -e "$binary" ] || [ -L "$binary" ]; then
    if [ -L "$binary" ] || [ ! -f "$binary" ] || [ -L "$receipt" ] || [ ! -f "$receipt" ]; then fail "Unmanaged gsc at $binary; refusing to overwrite/delete it."; fi
    [ "$(hash "$binary")" = "$(cat "$receipt")" ] || fail "gsc differs from its SearchProbe receipt; refusing to overwrite/delete it. Move your verified old install aside manually."
elif [ -e "$receipt" ] || [ -L "$receipt" ]; then
    fail 'Orphaned install receipt; inspect the managed directory before retrying.'
fi
if [ "$uninstall" = true ]; then
    rm -f "$binary" "$receipt"
    rmdir "$install_dir" 2>/dev/null || true
    printf 'SearchProbe binary removed. Credentials unchanged. Run auth logout BEFORE uninstall to revoke access and remove local credentials.\n'
    exit 0
fi
# Staging is on the destination filesystem so the final rename is atomic.
tmp=$(mktemp -d "$install_dir/.install.XXXXXXXX")
fetch() {
    if [ -n "$from_dir" ]; then
        cp "$from_dir/$1" "$tmp/$1"
    else
        curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --silent --show-error --location \
            --connect-timeout 15 --max-time 300 "${base_url%/}/$version/$1" -o "$tmp/$1"
    fi
}
fetch checksums.txt || fail 'Could not fetch checksums. Check that the pinned release is published and reachable, or use --from-dir with complete release assets.'
fetch "$asset" || fail "Could not fetch $asset. Check bundle/version and platform availability."
expected=$(awk -v name="$asset" '$2 == name {value=$1; count++} END {if (count != 1) exit 1; print value}' "$tmp/checksums.txt") || fail 'Missing or duplicate checksum entry.'
printf '%s\n' "$expected" | grep -Eq '^[a-f0-9]{64}$' || fail 'Missing, duplicate, or malformed checksum entry.'
[ "$(hash "$tmp/$asset")" = "$expected" ] || fail 'SHA256 mismatch; nothing installed.'
# Exact names and regular-file types only. Extract to stdout, never archive paths:
# traversal, symlinks, hardlinks, duplicate names, and extra members are rejected.
tar -tzf "$tmp/$asset" > "$tmp/names" || fail 'Invalid archive.'
printf 'gsc\nLICENSE\nTHIRD_PARTY_NOTICES.txt\n' > "$tmp/expected-names"
cmp -s "$tmp/names" "$tmp/expected-names" || fail 'Unexpected archive members.'
tar -tvzf "$tmp/$asset" > "$tmp/types" || fail 'Invalid archive metadata.'
awk 'substr($0,1,1) != "-" {bad=1} END {exit (bad || NR != 3)}' "$tmp/types" || fail 'Archive members must be regular files.'
tar -xOzf "$tmp/$asset" gsc > "$tmp/gsc" || fail 'Could not read gsc from archive.'
[ -s "$tmp/gsc" ] || fail 'Empty binary.'
chmod 755 "$tmp/gsc"
hash "$tmp/gsc" > "$tmp/receipt"
mv -f "$tmp/gsc" "$binary"
mv -f "$tmp/receipt" "$receipt"
printf '\nInstalled SearchProbe %s. No shell profiles or credentials changed.\n' "$version"
# shellcheck disable=SC2016 # Print the literal PATH variable for the user's shell.
printf 'Run now (and add this line to your shell profile if desired):\n  export PATH="%s:$PATH"\n' "$install_dir"
printf 'Then: hash -r; command -v gsc; gsc --version; gsc setup\n'
printf 'Restart your coding agent after changing its PATH. If Ghostscript needs gsc, use the absolute SearchProbe path instead.\n'
printf 'Update: rerun this installer with the new bundle/version. No automatic updates.\n'
printf 'Uninstall: "%s" auth logout, then sh install.sh --uninstall\n' "$binary"
