#!/usr/bin/python3
"""2026-09-30：以隔離狀態及指定 OAuth provider 探測 OMP，不保存憑證。"""

import argparse
from collections import Counter
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import sqlite3
import subprocess
import tempfile
import time


parser = argparse.ArgumentParser()
parser.add_argument("--case", choices=("smoke", "prompt-shape", "auth-missing", "write", "read-tools", "shell-write", "shell-outside", "delayed-control", "cancel"), required=True)
parser.add_argument("--pid-namespace", action="store_true")
parser.add_argument("--readonly-workspace", action="store_true")
options = parser.parse_args()
home = Path.home()
omp = Path(shutil.which("omp")).resolve()
bun = Path(shutil.which("bun")).resolve()
cli_sha = hashlib.sha256(omp.read_bytes()).hexdigest()
bun_sha = hashlib.sha256(bun.read_bytes()).hexdigest()
if cli_sha != "ca9b8832ea05299f02db83a6f479ab92e4646af7f0140b54f9c2592c07803441" or bun_sha != "a83d263767d839e4d2649ca8e35d07159c7afc99afdc96d731ced29e056dda0c":
    raise SystemExit("Binary differs from the OMP 18.4.4 / Bun 1.4.2 probe baseline")
source = sqlite3.connect(f"file:{home}/.omp/agent/agent.db?mode=ro", uri=True)
auth = sqlite3.connect(":memory:")
schema = list(source.execute(
    "SELECT type, sql FROM sqlite_master WHERE tbl_name LIKE 'auth_%' AND sql IS NOT NULL "
    "ORDER BY CASE type WHEN 'table' THEN 0 ELSE 1 END"
))
for _, sql in schema:
    auth.execute(sql)
for table, query in (
    ("auth_schema_version", "SELECT * FROM auth_schema_version"),
    ("auth_credentials", "SELECT * FROM auth_credentials WHERE provider='openai-codex' AND disabled_cause IS NULL"),
):
    for row in source.execute(query):
        auth.execute(f"INSERT INTO {table} VALUES ({','.join('?' for _ in row)})", row)
auth.commit()
if not auth.execute("SELECT count(*) FROM auth_credentials").fetchone()[0]:
    raise SystemExit("No active openai-codex OAuth credential")
credential_strings = []
for (data,) in auth.execute("SELECT data FROM auth_credentials"):
    credential_strings.extend(value for value in json.loads(data).values() if isinstance(value, str) and len(value) > 20)
if options.case == "auth-missing":
    auth.execute("DELETE FROM auth_credentials")
    auth.commit()
fd = os.memfd_create("omp-probe-auth")
os.write(fd, auth.serialize())
os.lseek(fd, 0, os.SEEK_SET)
source.close()

prompt = "Reply with exactly PROBE_OK"
no_tools = options.case in ("smoke", "prompt-shape", "auth-missing")
if options.case == "prompt-shape":
    prompt = "--not-a-flag\nThis prompt includes spaces and a newline. Reply with exactly PROMPT_OK."
elif not no_tools:
    prompt = "Create marker.txt in the current directory containing exactly PROBE_OK. Use a tool to actually write it; do not merely describe the action."
tools = ["--no-tools"] if no_tools else ["--tools", "write" if options.case == "write" else "read,grep,glob"]
if options.case in ("delayed-control", "cancel"):
    prompt = "Run exactly /usr/bin/python3 delayed.py using the bash tool, wait for it to finish, then reply DONE. Do not change the command or any files."
    tools = ["--tools", "bash", "--no-pty"]
if options.case == "shell-write":
    prompt = "Run exactly /usr/bin/python3 -c \"from pathlib import Path; Path('marker.txt').write_text('PROBE_OK')\" using the bash tool. Report the result; do not use another tool or change the command."
    tools = ["--tools", "bash", "--no-pty"]
config = {
    "retry.enabled": False, "retry.modelFallback": False, "retry.usageAwareFallback": False,
    "retry.waitForUsageReset": False, "retry.fallbackChains": {}, "memories.enabled": False,
    "compaction.enabled": False, "plan.defaultOnStartup": False,
}
try:
    with tempfile.TemporaryDirectory(prefix="asura-25-omp-") as directory, tempfile.TemporaryDirectory(prefix="asura-25-outside-") as outside_directory:
        root = Path(directory)
        workspace = root / "workspace"
        workspace.mkdir()
        if options.case in ("delayed-control", "cancel"):
            (workspace / "delayed.py").write_text(
                "import os, subprocess, time\nfrom pathlib import Path\n"
                "child = subprocess.Popen(['/usr/bin/python3', '-c', \"import time; from pathlib import Path; time.sleep(8); Path('delayed.txt').write_text('DELAYED')\"])\n"
                "Path('child.pid').write_text(str(child.pid))\n"
                "Path('ready').write_text(str(os.getpid()))\nchild.wait()\n"
            )
        (root / "config.json").write_text(json.dumps(config))
        if options.case == "shell-outside":
            target = Path(outside_directory) / "marker.txt"
            prompt = f"Run exactly /usr/bin/python3 -c \"from pathlib import Path; Path('{target}').write_text('PROBE_OK')\" using the bash tool. Report the result; do not use another tool or change the command."
            tools = ["--tools", "bash", "--no-pty"]
        cli_args = [str(bun), str(omp), "--model", "openai-codex/gpt-6-luna", "--thinking", "low",
                    "--config", str(root / "config.json"), "--no-session", "--no-lsp", "--no-title",
                    "--no-extensions", "--no-skills", "--no-rules", "--no-prewalk",
                    "--approval-mode", "yolo", *tools, "--mode", "json", "--print", "--", prompt]
        args = ["bwrap", "--ro-bind", "/", "/", "--dev-bind", "/dev", "/dev", "--proc", "/proc",
                "--tmpfs", str(home / ".omp"), "--dir", str(home / ".omp/agent"),
                "--bind-data", str(fd), str(home / ".omp/agent/agent.db"),
                "--bind", str(root), str(root), "--die-with-parent", "--chdir", str(workspace),
                "--clearenv", "--setenv", "HOME", str(home), "--setenv", "PATH", "/usr/bin:/bin",
                "--", *cli_args]
        if options.case == "shell-outside" and not options.readonly_workspace:
            mount_end = args.index("--die-with-parent")
            args[mount_end:mount_end] = ["--bind", outside_directory, outside_directory]
        if options.pid_namespace:
            args[1:1] = ["--unshare-pid"]
        if options.readonly_workspace:
            mount_end = args.index("--die-with-parent")
            args[mount_end:mount_end] = ["--ro-bind", str(workspace), str(workspace)]
        process = subprocess.Popen(args, pass_fds=(fd,), stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        timed_out = False
        cancellation = None
        if options.case == "cancel":
            deadline = time.monotonic() + 60
            while process.poll() is None and not (workspace / "ready").exists() and time.monotonic() < deadline:
                time.sleep(0.1)
            if (workspace / "ready").exists():
                child_pid = int((workspace / "child.pid").read_text())
                ready_pid = int((workspace / "ready").read_text())
                tree = []
                for stat_path in Path("/proc").glob("[0-9]*/stat"):
                    try:
                        fields = stat_path.read_text().rsplit(")", 1)[1].split()
                        tree.append({"pid": int(stat_path.parent.name), "ppid": int(fields[1]), "pgid": int(fields[2]), "state": fields[0]})
                    except (FileNotFoundError, ProcessLookupError, PermissionError):
                        pass
                descendants = {process.pid}
                while True:
                    expanded = descendants | {entry["pid"] for entry in tree if entry["ppid"] in descendants}
                    if expanded == descendants:
                        break
                    descendants = expanded
                before = [entry for entry in tree if entry["pid"] in descendants]
                if options.pid_namespace:
                    for entry in before:
                        status = Path(f"/proc/{entry['pid']}/status").read_text()
                        namespace_pid = next(int(line.split()[-1]) for line in status.splitlines() if line.startswith("NSpid:"))
                        if namespace_pid == child_pid:
                            child_host_pid = entry["pid"]
                        if namespace_pid == ready_pid:
                            parent_host_pid = entry["pid"]
                    child_pid, ready_pid = child_host_pid, parent_host_pid
                child_stat = Path(f"/proc/{child_pid}/stat")
                identity = child_stat.read_text().rsplit(")", 1)[1].split()[19] if child_stat.exists() else None
                groups = {"invocation": process.pid, "delayed_parent": os.getpgid(ready_pid), "delayed_child": os.getpgid(child_pid)}
                marker_before = (workspace / "delayed.txt").exists()
                ready_age = time.time() - (workspace / "ready").stat().st_mtime
                os.killpg(process.pid, signal.SIGKILL)
                time.sleep(10)
                current = child_stat.read_text().rsplit(")", 1)[1].split() if child_stat.exists() else None
                alive = current is not None and current[19] == identity and current[0] != "Z"
                cancellation = {"ready": True, "child_alive_after_10s": alive,
                                "groups_before_kill": groups, "processes_before_kill": before,
                                "marker_before_kill": marker_before, "ready_age_seconds": round(ready_age, 3),
                                "delayed_marker": (workspace / "delayed.txt").read_text() if (workspace / "delayed.txt").exists() else None}
                if alive:
                    os.kill(child_pid, signal.SIGKILL)
            else:
                cancellation = {"ready": False}
        try:
            stdout, stderr = process.communicate(timeout=90)
        except subprocess.TimeoutExpired:
            timed_out = True
            os.killpg(process.pid, signal.SIGKILL)
            stdout, stderr = process.communicate()
        if any(value.encode() in stdout or value.encode() in stderr for value in credential_strings):
            raise SystemExit("Credential appeared in CLI output; refusing to save evidence")
        events = []
        event_types = Counter()
        for line in stdout.decode(errors="replace").splitlines():
            try:
                event = json.loads(line)
                event_types[event.get("type")] += 1
                if event.get("type") == "message_end":
                    message = event.get("message", {})
                    events.append({"type": event["type"], "role": message.get("role"),
                                   "provider": message.get("provider"), "model": message.get("model"),
                                   "stopReason": message.get("stopReason"), "errorMessage": message.get("errorMessage"),
                                   "content": [{k: v for k, v in item.items() if k not in ("textSignature", "id")}
                                               for item in message.get("content", [])]})
            except ValueError:
                events.append({"non_json": line})
        marker = workspace / "marker.txt"
        print(json.dumps({"date": datetime.date.today().isoformat(), "case": options.case,
                          "pid_namespace": options.pid_namespace,
                          "readonly_workspace": options.readonly_workspace,
                          "script_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                          "version": subprocess.check_output([str(bun), str(omp), "--version"], text=True).strip(),
                          "cli_sha256": cli_sha,
                          "bun_version": subprocess.check_output([str(bun), "--version"], text=True).strip(),
                          "bun_sha256": bun_sha,
                          "argv": [arg.replace(str(root), "<probe>") for arg in cli_args], "config": config,
                          "exit_code": process.returncode, "timed_out": timed_out,
                          "marker": marker.read_text() if marker.exists() else None,
                          "outside_marker": (Path(outside_directory) / "marker.txt").read_text() if (Path(outside_directory) / "marker.txt").exists() else None,
                          "delayed_marker": (workspace / "delayed.txt").read_text() if (workspace / "delayed.txt").exists() else None,
                          "cancellation": cancellation,
                          "event_types": dict(event_types), "events": events,
                          "stderr": stderr.decode(errors="replace")}, ensure_ascii=False, indent=2))
finally:
    os.close(fd)
