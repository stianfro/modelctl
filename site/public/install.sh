#!/bin/sh
set -eu

fail() { printf 'modelctl: %s\n' "$*" >&2; exit 1; }
version=${MODELCTL_VERSION:-}
install_dir=${MODELCTL_INSTALL_DIR:-"$HOME/.local/bin"}
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version|--install-dir)
            [ "$#" -ge 2 ] || fail "$1 needs a value"
            case "$1" in --version) version=$2 ;; --install-dir) install_dir=$2 ;; esac
            shift 2 ;;
        --help) printf 'Usage: install.sh [--version vX.Y.Z] [--install-dir PATH]\n'; exit 0 ;;
        *) fail "unknown option: $1" ;;
    esac
done
[ -n "$install_dir" ] || fail 'install directory is empty'
case "$(uname -s)" in Linux) system=linux ;; Darwin) system=darwin ;; *) fail 'supported systems: Linux, macOS' ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) fail 'supported architectures: amd64, arm64' ;; esac
command -v curl >/dev/null || fail 'curl is required'
fetch() { curl --proto '=https' --proto-redir '=https' -fsSL --retry 3 --connect-timeout 15 --max-time 180 "$1" -o "$2"; }
tmp=$(mktemp -d)
staged=
cleanup() {
    rm -f "$tmp/release.json" "$tmp/archive.tar.gz" "$tmp/SHA256SUMS" "$tmp/modelctl"
    rmdir "$tmp"
    if [ -n "$staged" ]; then rm -f "$staged"; fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
if [ -z "$version" ]; then
    fetch https://api.github.com/repos/stianfro/modelctl/releases/latest "$tmp/release.json"
    version=$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/release.json")
fi
version=${version#v}
printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || fail 'expected a stable version: vX.Y.Z'
asset="modelctl_${version}_${system}_${arch}.tar.gz"
base="https://github.com/stianfro/modelctl/releases/download/v$version"
fetch "$base/$asset" "$tmp/archive.tar.gz"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS"
expected=$(awk -v name="$asset" '$2 == name { print $1 }' "$tmp/SHA256SUMS")
[ "${#expected}" -eq 64 ] || fail 'missing or invalid checksum'
case "$expected" in *[!0-9a-f]*) fail 'invalid checksum' ;; esac
if command -v sha256sum >/dev/null; then
    actual=$(sha256sum "$tmp/archive.tar.gz" | awk '{print $1}')
elif command -v shasum >/dev/null; then
    actual=$(shasum -a 256 "$tmp/archive.tar.gz" | awk '{print $1}')
else
    fail 'sha256sum or shasum is required'
fi
[ "$actual" = "$expected" ] || fail 'checksum mismatch'
tar -xOf "$tmp/archive.tar.gz" modelctl > "$tmp/modelctl"
[ -s "$tmp/modelctl" ] || fail 'archive has no binary'
mkdir -p "$install_dir"
staged=$(mktemp "$install_dir/.modelctl.XXXXXX")
cat "$tmp/modelctl" > "$staged"
chmod 755 "$staged"
mv -f "$staged" "$install_dir/modelctl"
staged=
printf 'Installed modelctl %s in %s\n' "$version" "$install_dir"
case ":$PATH:" in *":$install_dir:"*) ;; *) printf 'Add this directory to PATH: %s\n' "$install_dir" ;; esac
