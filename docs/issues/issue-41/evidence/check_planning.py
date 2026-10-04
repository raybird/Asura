"""2026-10-04：檢查 issue #41 規劃文件，非產品行為測試。"""

from pathlib import Path
import re

root = Path(__file__).resolve().parent.parent
readme = (root / "README.md").read_text()
scenarios = re.findall(r"@EXAUD-(\d{3})", readme)
approval = re.findall(r"^\| EXAUD-(\d{3}) \|", readme, re.M)
expected = {f"{i:03}" for i in range(1, 20)}
assert len(scenarios) == len(approval) == 19
assert set(scenarios) == set(approval) == expected
assert len(list(root.glob("*.md"))) == 4
for path in root.glob("*.md"):
    for target in re.findall(r"\]\(([^)]+)\)", path.read_text()):
        if not target.startswith("http"):
            assert (path.parent / target.split("#")[0]).exists(), (path, target)

technical = (root / "technical-analysis.md").read_text()
assert set(re.findall(r"^\| EA-TBD-(\d+) \|", technical, re.M)) == {
    str(i) for i in range(1, 17)
}
assert set(re.findall(r"^\| R(\d+) ", technical, re.M)) == {
    str(i) for i in range(1, 19)
}
assert "**分級**: Large" in readme and "**風險**: High" in readme
plan = (root / "implementation-plan.md").read_text()
for task in ("P0.1", "P1.1", "P2.1", "P3.1", "P4.1", "P5.1", "P5.2"):
    assert f"### Task {task}" in plan
assert len(re.findall(r"\| EXAUD-\d{3} \| 2026-10-04 \| 已核准 \|", readme)) == 19
print("PASS: files, links, 19 approved scenarios, EA-TBD 1-16, R1-18, seven tasks")
