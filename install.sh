#!/bin/sh
# Asura installer：下載正式 Release 並安裝 asura binary（Linux amd64／arm64，不需 root）。
# 用法：curl -fsSL https://raw.githubusercontent.com/raybird/Asura/main/install.sh | sh -s -- [--version vX.Y.Z] [--install-dir PATH]
set -eu

repo=raybird/Asura
usage='Usage: install.sh [--version vX.Y.Z] [--install-dir PATH]'

err() {
	printf 'asura-install: error: %s\n' "$*" >&2
	exit 1
}

usage_error() {
	printf '%s\n' "$usage" >&2
	exit 2
}

have() { command -v "$1" >/dev/null 2>&1; }

tag=
dir="$HOME/.local/bin"
while [ $# -gt 0 ]; do
	case "$1" in
	--version)
		[ $# -ge 2 ] || usage_error
		tag=$2
		shift 2
		;;
	--install-dir)
		[ $# -ge 2 ] || usage_error
		dir=$2
		shift 2
		;;
	-h | --help)
		printf '%s\n' "$usage"
		exit 0
		;;
	*) usage_error ;;
	esac
done

# 前置工具與平台檢查都在任何下載或檔案變更之前完成。
if have curl; then
	fetch() { curl -fsSL -o "$2" "$1"; }
elif have wget; then
	fetch() { wget -qO "$2" "$1"; }
else
	err "missing required tool: curl or wget"
fi
have tar || err "missing required tool: tar"
have sha256sum || have shasum || err "missing required tool: sha256sum or shasum"
have uname || err "missing required tool: uname"

os=$(uname -s)
machine=$(uname -m)
case "$os/$machine" in
Linux/x86_64) arch=amd64 ;;
Linux/aarch64 | Linux/arm64) arch=arm64 ;;
*) err "unsupported platform: $os/$machine (supported: Linux x86_64, aarch64)" ;;
esac

tmp=$(mktemp -d)
stage=
cleanup() {
	rm -rf "$tmp"
	if [ -n "$stage" ]; then rm -f "$stage"; fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM

if [ -z "$tag" ]; then
	fetch "https://api.github.com/repos/$repo/releases/latest" "$tmp/latest.json" || err "cannot query latest release"
	tag=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)
	[ -n "$tag" ] || err "cannot determine latest release"
fi

asset="asura_${tag}_linux_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$tag"
fetch "$base/asura_${tag}_checksums.txt" "$tmp/checksums.txt" || err "cannot download checksum manifest for $tag"
fetch "$base/$asset" "$tmp/$asset" || err "cannot download $asset"

root="asura_${tag}_linux_${arch}"
tar -xzf "$tmp/$asset" -C "$tmp" "$root/asura" || err "archive $asset does not contain $root/asura"

mkdir -p "$dir"
stage="$dir/.asura.tmp.$$"
cp "$tmp/$root/asura" "$stage"
chmod 0755 "$stage"
mv -f "$stage" "$dir/asura"
stage=

printf 'Installed asura %s to %s\n' "$tag" "$dir/asura"
case ":$PATH:" in
*":$dir:"*) ;;
*) printf 'Note: %s is not in PATH; add it to PATH to run asura.\n' "$dir" ;;
esac
