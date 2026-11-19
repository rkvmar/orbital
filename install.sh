#!/bin/sh
set -eu

cd "$(dirname "$0")"

dir=${INSTALL_DIR:-/usr/local/bin}
tmp=$(mktemp)
trap 'rm -f "$tmp"' 0 1 2 3 15
go build -o "$tmp" .

if [ -w "$dir" ]; then
	mkdir -p "$dir"
	install -m 755 "$tmp" "$dir/orbital"
else
	sudo mkdir -p "$dir"
	sudo install -m 755 "$tmp" "$dir/orbital"
fi
printf 'Installed orbital to %s/orbital\n' "$dir"

case ":$PATH:" in
	*":$dir:"*) ;;
	*) printf 'Add %s to your PATH to use orbital.\n' "$dir" >&2 ;;
esac
