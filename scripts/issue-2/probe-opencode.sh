#!/usr/bin/env bash
# scripts/issue-2/probe-opencode.sh
# 建立日期：2026-09-12
# 用途：Issue 2 Phase 1 (B-00) OpenCode 1.18.23 隔離探測與契約驗證

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROBE_DIR="${ASURA_PROBE_DIR:-"$HOME/.cache/asura-probe-issue-2"}"
INSTALL_DIR="${PROBE_DIR}/install"
OPENCODE_BIN="${INSTALL_DIR}/node_modules/.bin/opencode"
EVIDENCE_DIR="${REPO_ROOT}/docs/issues/issue-2/evidence/phase-1"
EXPECTED_VERSION="1.18.23"

mkdir -p "${EVIDENCE_DIR}"

cmd_install() {
  echo "=== [Phase 1 / P1-T2] 執行隔離安裝 ==="
  mkdir -p "${INSTALL_DIR}"
  if [ ! -x "${OPENCODE_BIN}" ]; then
    echo "正在安裝 opencode-ai@${EXPECTED_VERSION} 至 ${INSTALL_DIR} ..."
    npm install --prefix "${INSTALL_DIR}" "opencode-ai@${EXPECTED_VERSION}" --no-audit --no-fund
  fi

  if [ ! -x "${OPENCODE_BIN}" ]; then
    echo "錯誤：找不到執行檔 ${OPENCODE_BIN}" >&2
    exit 1
  fi

  local current_ver
  current_ver="$("${OPENCODE_BIN}" --version 2>/dev/null || true)"
  if [ "${current_ver}" != "${EXPECTED_VERSION}" ]; then
    echo "錯誤：版本不符。預期 ${EXPECTED_VERSION}，實際 ${current_ver}" >&2
    exit 1
  fi

  echo "ISOLATION_ROOT=${INSTALL_DIR}"
  echo "OPENCODE_BIN=${OPENCODE_BIN}"
  echo "VERSION=${current_ver}"
  echo "STATUS=INSTALLED_AND_VERIFIED"
}

check_installed() {
  if [ ! -x "${OPENCODE_BIN}" ]; then
    echo "請先執行：bash scripts/issue-2/probe-opencode.sh install" >&2
    exit 1
  fi
  local current_ver
  current_ver="$("${OPENCODE_BIN}" --version 2>/dev/null || true)"
  if [ "${current_ver}" != "${EXPECTED_VERSION}" ]; then
    echo "錯誤：版本不符。預期 ${EXPECTED_VERSION}，實際 ${current_ver}" >&2
    exit 1
  fi
}

cmd_events() {
  check_installed
  echo "=== [Phase 1 / P1-T4] 執行事件格式探測 ==="
  local ws="${PROBE_DIR}/ws-events"
  rm -rf "${ws}" && mkdir -p "${ws}"
  local cfg="${PROBE_DIR}/cfg-events"
  rm -rf "${cfg}" && mkdir -p "${cfg}"

  local sample_out="${EVIDENCE_DIR}/events_probe.jsonl"
  local sample_err="${EVIDENCE_DIR}/events_error.log"

  echo "探測正常 JSON 事件串流..."
  OPENCODE_CONFIG_DIR="${cfg}" \
  OPENCODE_DISABLE_AUTOUPDATE=1 \
  "${OPENCODE_BIN}" run --format json --dir "${ws}" "請回覆一句話：'OpenCode 契約探測成功'，不要呼叫工具" \
    > "${sample_out}" 2> "${sample_err}" || true

  # 驗證輸出是否包含 step_start, text, step_finish
  if grep -q '"type":"step_start"' "${sample_out}" && grep -q '"type":"text"' "${sample_out}" && grep -q '"type":"step_finish"' "${sample_out}"; then
    echo "事件串流格式符合預期（包含 step_start, text, step_finish）。"
  else
    echo "警告：事件串流可能缺少預期結構。" >&2
  fi

  # 探測結構化錯誤或非零退出
  echo "探測錯誤參數與非零退出碼..."
  set +e
  "${OPENCODE_BIN}" run --invalid-option-for-test > "${EVIDENCE_DIR}/invalid_opt.log" 2>&1
  local err_exit=$?
  set -e
  echo "錯誤選項測試退出碼: ${err_exit} (預期非 0)"
  echo "事件樣本已保存至 ${EVIDENCE_DIR}/events_probe.jsonl"
}

cmd_permissions() {
  check_installed
  echo "=== [Phase 1 / P1-T3] 執行權限契約探測 (INT-007 / INT-002) ==="
  local ws="${PROBE_DIR}/ws-permissions"
  rm -rf "${ws}" && mkdir -p "${ws}"
  local cfg="${PROBE_DIR}/cfg-permissions"
  rm -rf "${cfg}" && mkdir -p "${cfg}"

  local perm_log="${EVIDENCE_DIR}/permissions_probe.log"
  echo "=== 權限探測紀錄 $(date -Iseconds) ===" > "${perm_log}"

  # 1. 允許操作驗證：修改專案檔案
  echo "測試允許操作：在核准專案目錄下修改檔案..."
  echo "Original content" > "${ws}/target.txt"
  OPENCODE_CONFIG_DIR="${cfg}" \
  OPENCODE_DISABLE_AUTOUPDATE=1 \
  "${OPENCODE_BIN}" run --format json --dir "${ws}" "請在 target.txt 增加一行 'appended line'" >> "${perm_log}" 2>&1

  if grep -q "appended line" "${ws}/target.txt"; then
    echo "允許操作成功：target.txt 已正確修改。" | tee -a "${perm_log}"
  else
    echo "錯誤：允許操作失敗，檔案未修改。" | tee -a "${perm_log}" >&2
    exit 1
  fi

  # 2. 限制操作驗證：拒絕 bash 執行 rm/外發
  echo "測試限制操作：透過 OPENCODE_PERMISSION 阻擋危險工具 (bash)..."
  echo "Critical file" > "${ws}/critical.txt"
  set +e
  OPENCODE_CONFIG_DIR="${cfg}" \
  OPENCODE_PERMISSION='{"bash":"deny"}' \
  OPENCODE_DISABLE_AUTOUPDATE=1 \
  "${OPENCODE_BIN}" run --format json --dir "${ws}" "請使用 bash 執行 rm -f critical.txt" >> "${perm_log}" 2>&1
  set -e

  if [ -f "${ws}/critical.txt" ]; then
    echo "限制操作成功阻擋：critical.txt 完好無損，未被刪除。" | tee -a "${perm_log}"
  else
    echo "錯誤：限制操作未被阻擋，檔案被刪除！" | tee -a "${perm_log}" >&2
    exit 1
  fi

  echo "權限探測日誌已保存至 ${perm_log}"
}

cmd_cancellation() {
  check_installed
  echo "=== [Phase 1 / P1-T4] 執行進程取消契約探測 (INT-005) ==="
  local ws="${PROBE_DIR}/ws-cancel"
  rm -rf "${ws}" && mkdir -p "${ws}"
  local cfg="${PROBE_DIR}/cfg-cancel"
  rm -rf "${cfg}" && mkdir -p "${cfg}"

  local cancel_log="${EVIDENCE_DIR}/cancellation_probe.log"
  echo "=== 取消探測紀錄 $(date -Iseconds) ===" > "${cancel_log}"

  # 啟動背景不相關工作 B
  sleep 60 &
  local pid_b=$!
  echo "背景不相關工作 B 已啟動 (PID: ${pid_b})" | tee -a "${cancel_log}"

  # 使用 setsid 建立獨立進程組啟動任務 A
  echo "啟動受控長任務 A (建立獨立進程組)..." | tee -a "${cancel_log}"
  setsid bash -c "
    OPENCODE_CONFIG_DIR='${cfg}' \
    OPENCODE_DISABLE_AUTOUPDATE=1 \
    '${OPENCODE_BIN}' run --format json --dir '${ws}' '請詳細撰寫一篇一萬字關於分散式系統一致性演算法的長文論文'
  " > "${PROBE_DIR}/cancel_task_a.out" 2>&1 &
  local pid_a=$!

  # 等待程序初始化與產生進程樹
  sleep 2
  local pgid_a
  pgid_a="$(ps -o pgid= -p "${pid_a}" | tr -d ' ')"
  echo "任務 A 正在執行 (PID: ${pid_a}, PGID: ${pgid_a})" | tee -a "${cancel_log}"

  # 向 PGID 發送 SIGTERM 進行取消
  echo "向任務 A 進程組 -${pgid_a} 發送 SIGTERM 取消信號..." | tee -a "${cancel_log}"
  kill -TERM "-${pgid_a}" 2>/dev/null || true
  sleep 1

  # 檢查任務 A 是否已退出
  if ps -o pgid= | grep -w -q "${pgid_a}"; then
    echo "SIGTERM 後仍有進程存活，發送 SIGKILL..." | tee -a "${cancel_log}"
    kill -KILL "-${pgid_a}" 2>/dev/null || true
    sleep 1
  fi

  local alive_a=0
  if ps -o pgid= | grep -w -q "${pgid_a}"; then
    alive_a=1
  fi

  # 檢查不相關工作 B 是否正常存活
  local alive_b=0
  if kill -0 "${pid_b}" 2>/dev/null; then
    alive_b=1
  fi

  # 清理工作 B
  kill -9 "${pid_b}" 2>/dev/null || true

  if [ "${alive_a}" -eq 0 ] && [ "${alive_b}" -eq 1 ]; then
    echo "取消契約驗證成功：任務 A 進程樹完全回收，不相關工作 B 保持存活不受影響！" | tee -a "${cancel_log}"
  else
    echo "錯誤：任務 A 殘留=${alive_a}, 工作 B 存活=${alive_b}" | tee -a "${cancel_log}" >&2
    exit 1
  fi

  echo "取消探測紀錄已保存至 ${cancel_log}"
}

case "${1:-}" in
  install)
    cmd_install
    ;;
  events)
    cmd_events
    ;;
  permissions)
    cmd_permissions
    ;;
  cancellation)
    cmd_cancellation
    ;;
  all)
    cmd_install
    cmd_events
    cmd_permissions
    cmd_cancellation
    ;;
  *)
    echo "Usage: $0 {install|permissions|events|cancellation|all}" >&2
    exit 1
    ;;
esac
