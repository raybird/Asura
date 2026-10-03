"""2026-10-03：檢查 issue 33 規劃文件的連結、編號與核准狀態。"""

from pathlib import Path
import re

root = Path(__file__).resolve().parents[1]
files = [root / name for name in (
    "README.md", "requirement-analysis.md", "technical-analysis.md", "implementation-plan.md"
)]
for path in files:
    text = path.read_text()
    for target in re.findall(r"\]\(([^)]+)\)", text):
        if target.startswith("https://"):
            continue
        local = target.split("#", 1)[0]
        assert (path.parent / local).exists(), (path.name, target)
    assert "<!--" not in text, path.name
    assert "2026-10-03" in text, path.name
readme = files[0].read_text()
scenarios = re.findall(r"@RTCAP-(\d{3})", readme)
approvals = re.findall(r"\| RTCAP-(\d{3}) \| 2026-10-03 \| 已核准 \|", readme)
expected = [f"{n:03d}" for n in range(1, 17)]
assert scenarios == expected, scenarios
assert approvals == expected, approvals
for n in range(1, 11):
    assert f"| RC-TBD-{n} |" in readme, n
assert "**分級**: Large" in readme
assert "**風險**: High" in readme
print("PASS: 四份文件存在；相對連結有效；無範本註解。")
print("PASS: RTCAP-001～016 與核准表一對一，全部已核准。")
print("PASS: RC-TBD-1～10 齊備，Large／High metadata 與日期明確。")
print("限制：靜態結構檢查不代表設計已核准或 production 行為已驗證。")
