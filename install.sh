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
if have sha256sum; then
	sha256() { sha256sum "$1" | awk '{print $1}'; }
elif have shasum; then
	sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
	err "missing required tool: sha256sum or shasum"
fi
have uname || err "missing required tool: uname"

os=$(uname -s)
machine=$(uname -m)
case "$os/$machine" in
Linux/x86_64) arch=amd64 ;;
Linux/aarch64 | Linux/arm64) arch=arm64 ;;
*) err "unsupported platform: $os/$machine (supported: Linux x86_64, aarch64)" ;;
esac

# 只接受一般檔案作為安裝目標；symlink／目錄等保持原樣，由使用者決定。
target="$dir/asura"
if [ -L "$target" ] || { [ -e "$target" ] && [ ! -f "$target" ]; }; then
	err "$target is not a regular file ($(ls -ld "$target")); remove it or choose another --install-dir"
fi

check_version() {
	printf '%s\n' "$1" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || err "invalid version: $1"
	ver=${1#v}
	major=${ver%%.*}
	rest=${ver#*.}
	minor=${rest%%.*}
	# v1.1.0 起的 Release 才含 version metadata，安裝後可核對版本。
	if [ "$major" -eq 0 ] || { [ "$major" -eq 1 ] && [ "$minor" -eq 0 ]; }; then
		err "$1 predates installer support (v1.1.0+); download it manually from https://github.com/$repo/releases/tag/$1"
	fi
}
if [ -n "$tag" ]; then check_version "$tag"; fi

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
	check_version "$tag"
fi

asset="asura_${tag}_linux_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$tag"
fetch "$base/asura_${tag}_checksums.txt" "$tmp/checksums.txt" || err "cannot download checksum manifest for $tag"
fetch "$base/$asset" "$tmp/$asset" || err "cannot download $asset"

entries=$(awk -v f="$asset" '$2 == f' "$tmp/checksums.txt" | wc -l | tr -d ' ')
[ "$entries" -eq 1 ] || err "checksum manifest has $entries entries for $asset"
expected=$(awk -v f="$asset" '$2 == f {print $1}' "$tmp/checksums.txt")
actual=$(sha256 "$tmp/$asset")
[ "$actual" = "$expected" ] || err "checksum mismatch for $asset"

# archive 只能含 root 底下的一般檔案與目錄；不接受絕對路徑、..、symlink 或其他型別。
root="asura_${tag}_linux_${arch}"
tar -tzf "$tmp/$asset" >"$tmp/names" || err "cannot read archive $asset"
tar -tvzf "$tmp/$asset" >"$tmp/listing" || err "cannot read archive $asset"
while IFS= read -r name; do
	case "$name" in
	"$root/" | "$root"/*) ;;
	*) err "unsafe archive entry: $name" ;;
	esac
	case "/$name/" in
	*/../*) err "unsafe archive entry: $name" ;;
	esac
done <"$tmp/names"
if awk 'substr($0, 1, 1) != "-" && substr($0, 1, 1) != "d"' "$tmp/listing" | grep -q .; then
	err "unsafe archive entry type in $asset"
fi
grep -qx "$root/asura" "$tmp/names" || err "archive $asset does not contain $root/asura"
tar -xzf "$tmp/$asset" -C "$tmp" "$root/asura" || err "cannot extract $root/asura"

mkdir -p "$dir" || err "cannot write to $dir"
stage="$dir/.asura.tmp.$$"
cp "$tmp/$root/asura" "$stage" 2>/dev/null || err "cannot write to $dir"
chmod 0755 "$stage"
reported=$("$stage" version --json 2>/dev/null) || err "staged binary cannot report its version"
for want in "\"version\":\"$tag\"" '"os":"linux"' "\"arch\":\"$arch\""; do
	case "$reported" in
	*"$want"*) ;;
	*) err "staged binary reports $reported, expected $tag linux/$arch" ;;
	esac
done
mv -f "$stage" "$target" || err "cannot replace $target"
stage=

printf 'Installed asura %s to %s\n' "$tag" "$target"
case ":$PATH:" in
*":$dir:"*) ;;
*) printf 'Note: %s is not in PATH; add it to PATH to run asura.\n' "$dir" ;;
esac
