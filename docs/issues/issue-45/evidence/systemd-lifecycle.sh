#!/bin/sh
# 2026-10-09：issue-45 Task 5.3，真實 systemd user bus 上的 asura service lifecycle 驗證（DIST-013）。
# 會暫時建立 ~/.config/systemd/user/asura.service 與測試用 drop-in，並實際啟動、停止、強制結束 daemon；
# 結束時 uninstall 並移除所有暫存。CLI 以暫存的 XDG_CONFIG_HOME 讀 config（2026-10-09 review M2：
# unit 必須落在 user manager 搜尋的目錄並固定 XDG_CONFIG_HOME），DB／socket 位於暫存目錄。
# 只在 unit 與 drop-in 都不存在時執行。用法（repo 根目錄）：ASURA_LIFECYCLE_CONFIRM=1 sh docs/issues/issue-45/evidence/systemd-lifecycle.sh
set -eu

[ "${ASURA_LIFECYCLE_CONFIRM:-}" = 1 ] || {
	echo "set ASURA_LIFECYCLE_CONFIRM=1 to modify your systemd user session" >&2
	exit 2
}
unset ASURA_SOCKET
unit="$HOME/.config/systemd/user/asura.service"
dropin="$unit.d"
for p in "$unit" "$dropin"; do
	if [ -e "$p" ]; then
		echo "refuse: $p already exists" >&2
		exit 1
	fi
done

work=$(mktemp -d /tmp/asura-lc.XXXXXX)
A="$work/asura"
export XDG_CONFIG_HOME="$work/xc"
cfgdir="$XDG_CONFIG_HOME/asura"
cleanup() {
	"$A" service uninstall >/dev/null 2>&1 || true
	rm -rf "$dropin"
	rm -f "$unit"
	systemctl --user daemon-reload
	systemctl --user reset-failed asura.service >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

prop() { systemctl --user show asura.service -p "$1" --value; }
wait_for() { # wait_for <秒> <命令…>：在期限內等待命令成功
	limit=$1
	shift
	tries=0
	while ! "$@" >/dev/null 2>&1; do
		tries=$((tries + 1))
		[ "$tries" -le $((limit * 5)) ] || return 1
		sleep 0.2
	done
}
check() { # check <描述> <條件>：條件整段以 eval 求值
	if eval "$2"; then echo "PASS: $1"; else
		echo "FAIL: $1" >&2
		exit 1
	fi
}
is_state() { [ "$(prop ActiveState)" = "$1" ]; }
task_status_is() { "$A" status "$id" | grep -q "Status:    $1"; }
no_stub_process() { ! pgrep -f "$work/stubbin" >/dev/null; }
restarted_from() { [ "$(prop NRestarts)" -gt "$1" ] && is_state active; }

go build -o "$A" ./cmd/asura
mkdir -p "$work/stubbin" "$work/project" "$work/data" "$work/run"
cat >"$work/stubbin/codex" <<'EOF'
#!/bin/sh
# stub runtime：版本探測回固定版本；執行任務時長時間停留以模擬長任務。
[ "$1" = --version ] && echo 'codex-cli 0.155.0' && exit 0
exec sleep 600
EOF
chmod 0755 "$work/stubbin/codex"
mkdir -p -m 0700 "$XDG_CONFIG_HOME" "$cfgdir"
cat >"$cfgdir/config.toml" <<EOF
schema_version = 1
allowed_roots = ["$work/project"]
db = "$work/data/asura.db"
socket = "$work/run/asura.sock"
EOF
chmod 0600 "$cfgdir/config.toml"
linger_before=$(loginctl show-user "$(id -un)" -p Linger --value)

echo "== install"
"$A" service install
# 測試專用 drop-in：讓 daemon 找到 stub codex（正式 unit 不含 PATH）。
mkdir -p "$dropin"
printf '[Service]\nEnvironment="PATH=%s/stubbin:/usr/bin:/bin"\n' "$work" >"$dropin/test-path.conf"
systemctl --user daemon-reload
for p in KillMode TimeoutStopUSec Restart RestartUSec StartLimitIntervalUSec StartLimitBurst UnitFileState FragmentPath; do
	echo "$p=$(prop "$p")"
done
check "unit properties" '[ "$(prop KillMode)" = mixed ] && [ "$(prop TimeoutStopUSec)" = 1min ] && [ "$(prop Restart)" = on-failure ] && [ "$(prop RestartUSec)" = 5s ] && [ "$(prop StartLimitIntervalUSec)" = 1min ] && [ "$(prop StartLimitBurst)" = 3 ]'
check "unit has no secret or linger directive" '! grep -Eiq "linger|key|token|secret" "$unit"'
check "unit in manager directory pins the CLI config home" 'grep -qx "Environment=\"XDG_CONFIG_HOME=$XDG_CONFIG_HOME\"" "$unit"'

echo "== start and long task"
"$A" service start
check "daemon socket ready" 'wait_for 15 test -S "$work/run/asura.sock"'
"$A" service status
id=$("$A" ask --backend codex --workspace "$work/project" "long running task" | sed -n 's/^Task ID: //p')
echo "task=$id"
check "task running" 'wait_for 20 task_status_is running'

echo "== stop waits at most 60s then reclaims the unit"
start=$(date +%s)
"$A" service stop
elapsed=$(($(date +%s) - start))
echo "stop elapsed=${elapsed}s"
check "stop bounded by TimeoutStopSec (55-75s)" '[ "$elapsed" -ge 55 ] && [ "$elapsed" -le 75 ]'
check "inactive after stop and no stub process left" '! is_state active && no_stub_process'

echo "== interrupted task marked failed on next start, not rerun"
"$A" service start
check "daemon socket ready" 'wait_for 15 test -S "$work/run/asura.sock"'
"$A" status "$id" | sed -n '1,3p'
check "interrupted task failed" 'task_status_is failed'
check "task not rerun" 'no_stub_process'

echo "== manual stop does not restart"
restarts=$(prop NRestarts)
"$A" service stop
sleep 8
check "still inactive 8s after manual stop" 'is_state inactive && [ "$(prop NRestarts)" = "$restarts" ]'

echo "== crash restarts after 5s, at most 3 starts within 60s"
# 先前的手動 start 也計入 60 秒內的啟動次數；reset-failed 重設計數後只觀察 crash 重啟。
systemctl --user reset-failed asura.service
"$A" service start
check "active" 'wait_for 15 is_state active'
for i in 1 2 3; do
	pid=$(prop MainPID)
	before=$(prop NRestarts)
	t0=$(date +%s)
	kill -KILL "$pid"
	if [ "$i" -lt 3 ]; then
		check "crash $i restarted" 'wait_for 15 restarted_from "$before"'
		delay=$(($(date +%s) - t0))
		echo "crash $i: restart after ${delay}s (NRestarts=$(prop NRestarts))"
		check "crash $i restart delay follows RestartSec=5s (4-8s)" '[ "$delay" -ge 4 ] && [ "$delay" -le 8 ]'
	fi
done
# 第 4 次啟動被拒：unit 停在 failed、不再有 MainPID，journal 留有 systemd 的限流訊息。
sleep 8
systemctl --user show asura.service -p ActiveState -p Result -p NRestarts -p MainPID
check "third crash within 60s stays failed without a 4th start" 'is_state failed && [ "$(prop MainPID)" = 0 ]'
check "systemd logged the start rate limit" 'journalctl --user -u asura.service --since "@$t0" --no-pager | grep -q "Start request repeated too quickly"'
systemctl --user reset-failed asura.service

echo "== uninstall keeps config and data"
cfg_sum=$(sha256sum "$cfgdir/config.toml")
db_sum=$(sha256sum "$work/data/asura.db")
"$A" service uninstall
check "unit removed" 'test ! -e "$unit"'
check "config and database unchanged" '[ "$(sha256sum "$cfgdir/config.toml")" = "$cfg_sum" ] && [ "$(sha256sum "$work/data/asura.db")" = "$db_sum" ]'
check "task still readable" 'task_status_is failed'
check "linger unchanged" '[ "$(loginctl show-user "$(id -un)" -p Linger --value)" = "$linger_before" ]'
echo "== LIFECYCLE OK"
