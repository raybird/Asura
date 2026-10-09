#!/bin/sh
# 2026-10-09：issue-45 Task 6.2，clean Linux onboarding 端到端驗證（HTTP fixture）。
# 以 build-archives.sh 建出 fixture Release，透過本機 HTTP server 提供；容器內沒有 Go 與 repo，
# 以非 root 使用者執行 install → version → doctor → setup → standalone daemon。
# GitHub URL 由容器內的 wget wrapper 改寫到 fixture，runtime 為 stub；不代表正式 Release 或真實 runtime。
# 需求：docker、python3。用法（repo 根目錄）：sh scripts/release/onboarding-e2e.sh [image]
set -eu

image=${1:-alpine:3.20.3}
tag=v1.1.0
port=18745
work=$(mktemp -d)
server=
cleanup() {
	if [ -n "$server" ]; then kill "$server" 2>/dev/null || true; fi
	rm -rf "$work"
}
trap cleanup EXIT

commit=$(git rev-parse HEAD)
dl="$work/www/gh/raybird/Asura/releases/download/$tag"
mkdir -p "$dl" "$work/www/api/repos/raybird/Asura/releases" "$work/www/raw/raybird/Asura/main"
sh scripts/release/build-archives.sh "$tag" "$commit" "$dl"
printf '{"tag_name": "%s", "prerelease": false, "draft": false}\n' "$tag" >"$work/www/api/repos/raybird/Asura/releases/latest"
cp install.sh "$work/www/raw/raybird/Asura/main/install.sh"

cat >"$work/wget" <<EOF
#!/bin/sh
# E2E 專用：把 GitHub URL 改寫到本機 HTTP fixture，其餘參數原樣交給 busybox wget。
n=\$#
for a do
	case \$a in
	https://*) a=\$(printf '%s' "\$a" | sed -e 's#^https://api.github.com/#http://127.0.0.1:$port/api/#' -e 's#^https://github.com/#http://127.0.0.1:$port/gh/#' -e 's#^https://raw.githubusercontent.com/#http://127.0.0.1:$port/raw/#') ;;
	esac
	set -- "\$@" "\$a"
done
shift \$n
exec /bin/busybox wget "\$@"
EOF
chmod 0755 "$work/wget"

cat >"$work/codex" <<'EOF'
#!/bin/sh
# stub runtime：只回應固定版本，不代表真實 Codex。
[ "$1" = --version ] && echo 'codex-cli 0.155.0' && exit 0
exit 1
EOF
chmod 0755 "$work/codex"

cat >"$work/steps.sh" <<EOF
set -eu
cd "\$HOME"
echo "== whoami: \$(id -u) / no go: \$(command -v go || echo absent)"
wget -qO- https://raw.githubusercontent.com/raybird/Asura/main/install.sh | sh
asura=\$HOME/.local/bin/asura
json=\$("\$asura" version --json)
echo "== version: \$json"
case "\$json" in *'"version":"$tag"'*'"arch":'*) ;; *'"arch":'*'"version":"$tag"'*) ;; *) echo "version mismatch"; exit 1 ;; esac
case "\$json" in *'"commit":"$commit"'*) ;; *) echo "commit mismatch"; exit 1 ;; esac
mkdir -p "\$HOME/stubbin" "\$HOME/project"
cp /fixture/codex "\$HOME/stubbin/codex"
export PATH="\$HOME/stubbin:\$PATH"
set +e
"\$asura" doctor
code=\$?
set -e
echo "== doctor exit: \$code"
[ "\$code" -eq 0 ]
"\$asura" setup --allowed-roots "\$HOME/project"
"\$asura" daemon >"\$HOME/daemon.log" 2>&1 &
pid=\$!
i=0
while [ ! -S "\$HOME/.local/state/asura/asura.sock" ] && [ \$i -lt 50 ]; do sleep 0.2; i=\$((i + 1)); done
cat "\$HOME/daemon.log"
"\$asura" runtime inventory
kill -TERM \$pid
wait \$pid
echo "== daemon exit: \$?"
echo "== ONBOARDING OK"
EOF

python3 -m http.server "$port" --bind 127.0.0.1 --directory "$work/www" >"$work/http.log" 2>&1 &
server=$!
sleep 1

docker run --rm --network host --user "$(id -u):$(id -g)" \
	--tmpfs /home/tester:rw,exec,mode=1777 -e HOME=/home/tester \
	-v "$work/wget:/usr/local/bin/wget:ro" -v "$work/codex:/fixture/codex:ro" -v "$work/steps.sh:/fixture/steps.sh:ro" \
	"$image" sh /fixture/steps.sh
echo "== HTTP requests served:"
cat "$work/http.log"
