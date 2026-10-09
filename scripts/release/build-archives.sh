#!/bin/sh
# 2026-10-09：issue-45，release.yml 與測試共用的 archive 建置。
# 命名與 archive root 沿用 v1.0.0；注入 tag／commit 供 asura version 回報。
# 用法（repo 根目錄）：sh scripts/release/build-archives.sh <tag> <commit> <outdir>
set -eu

tag=$1
commit=$2
out=$3

mkdir -p "$out"
for arch in amd64 arm64; do
  name="asura_${tag}_linux_${arch}"
  mkdir -p "$out/$name"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=${tag} -X main.commit=${commit}" -o "$out/$name/asura" ./cmd/asura
  cp LICENSE README.md CHANGELOG.md "$out/$name/"
  tar -C "$out" -czf "$out/$name.tar.gz" "$name"
  rm -rf "${out:?}/$name"
done
(cd "$out" && sha256sum *.tar.gz > "asura_${tag}_checksums.txt")
