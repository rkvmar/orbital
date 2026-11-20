#!/bin/sh
set -eu

repo=${ORBITAL_REPO:-rkvmar/orbital}
version=${ORBITAL_VERSION:-latest}
dir=${ORBITAL_INSTALL_DIR:-$HOME/.local/bin}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
	x86_64|amd64) arch=amd64 ;;
	arm64|aarch64) arch=arm64 ;;
	*) printf 'orbital: unsupported architecture %s\n' "$(uname -m)" >&2; exit 1 ;;
esac
case "$os" in
	darwin|linux) ;;
	*) printf 'orbital: unsupported OS %s\n' "$os" >&2; exit 1 ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
else
	printf 'orbital: curl or wget is required\n' >&2
	exit 1
fi

if command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
elif command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | awk '{print $1}'; }
else
	sha256() { printf ''; }
fi

asset="orbital-${os}-${arch}.tar.gz"
base="https://github.com/${repo}/releases"
if [ "$version" = latest ]; then
	asset_url="${base}/latest/download/${asset}"
	sums_url="${base}/latest/download/SHA256SUMS"
else
	asset_url="${base}/download/${version}/${asset}"
	sums_url="${base}/download/${version}/SHA256SUMS"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' 0 1 2 3 15

printf 'Downloading %s...\n' "$asset"
fetch "$asset_url" "$tmp/$asset"

if fetch "$sums_url" "$tmp/SHA256SUMS" 2>/dev/null; then
	expected=$(awk -v f="$asset" '$2 == f || $2 == "*/" f {print $1; exit}' "$tmp/SHA256SUMS")
	if [ -n "$expected" ]; then
		actual=$(sha256 "$tmp/$asset")
		if [ -z "$actual" ]; then
			printf 'orbital: no sha256 tool, skipping checksum verification\n' >&2
		elif [ "$actual" != "$expected" ]; then
			printf 'orbital: checksum mismatch for %s\n' "$asset" >&2
			exit 1
		fi
	fi
fi

tar -xzf "$tmp/$asset" -C "$tmp"
bin="$tmp/orbital"
[ -f "$bin" ] || bin="$tmp/orbital-${os}-${arch}"
[ -f "$bin" ] || { printf 'orbital: binary not found in %s\n' "$asset" >&2; exit 1; }

mkdir -p "$dir"
install -m 755 "$bin" "$dir/orbital"
printf 'Installed orbital to %s/orbital\n' "$dir"

case ":${PATH}:" in
	*":${dir}:"*) ;;
	*) printf 'Add %s to your PATH to use orbital.\n' "$dir" >&2 ;;
esac
