#!/usr/bin/python3
"""2026-09-30：固定模型的 Antigravity 隔離契約探測；不保存登入憑證。"""
import argparse
import datetime
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import select
import socket
import threading
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time

script_sha = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
parser = argparse.ArgumentParser()
parser.add_argument('--case', choices=('smoke', 'auth-missing', 'short-timeout', 'partial', 'side-effect-timeout', 'proxy-smoke', 'proxy-failure', 'write', 'delayed-control', 'cancel'), required=True)
parser.add_argument('--readonly-workspace', action='store_true')
parser.add_argument('--isolated-home', action='store_true')
parser.add_argument('--isolated-ipc', action='store_true')
parser.add_argument('--access-only', action='store_true')
parser.add_argument('--auth-source', choices=('file', 'keyring'), default='file')
parser.add_argument('--binary-path', default='/home/kevin/.local/bin/agy')
options = parser.parse_args()
cli = Path(options.binary_path).resolve()
cli_sha = hashlib.sha256(cli.read_bytes()).hexdigest()
if cli_sha != '574b0234656c44564f3ff3b013225c86f14ae79b67d0f3b45f6aaf1b1625440e':
    raise SystemExit('Binary differs from CLI 1.2.13 probe baseline')
state = Path.home() / '.gemini/antigravity-cli'
token = (subprocess.check_output(['secret-tool', 'lookup', 'service', 'gemini', 'username', 'antigravity'])
         if options.auth_source == 'keyring' else (state / 'antigravity-oauth-token').read_bytes())
original_token = token
if options.access_only:
    credential = json.loads(token)
    credential['token'].pop('refresh_token', None)
    token = json.dumps(credential).encode()
fd = os.memfd_create('agy-probe-auth')
os.write(fd, token)
os.lseek(fd, 0, os.SEEK_SET)
proxy = None
proxy_connections = []
proxy_sockets = []
proxy_failed = threading.Event()
if options.case in ('proxy-smoke', 'proxy-failure'):
    class TunnelHandler(BaseHTTPRequestHandler):
        def do_CONNECT(self):
            proxy_connections.append({'after_fault': proxy_failed.is_set()})
            if proxy_failed.is_set():
                self.send_error(503, 'PROBE_TRANSPORT_FAILURE')
                return
            host, port = self.path.rsplit(':', 1)
            remote = socket.create_connection((host, int(port)), timeout=10)
            pair = (self.connection, remote)
            proxy_sockets.append(pair)
            try:
                self.send_response(200, 'Connection established')
                self.end_headers()
                remote.settimeout(None)
                while not proxy_failed.is_set():
                    readable, _, _ = select.select(pair, [], [], 1)
                    for source in readable:
                        data = source.recv(65536)
                        if not data:
                            return
                        (remote if source is self.connection else self.connection).sendall(data)
            except (OSError, ValueError):
                pass
            finally:
                remote.close()
        def log_message(self, *args):
            pass
    proxy = ThreadingHTTPServer(('127.0.0.1', 0), TunnelHandler)
    threading.Thread(target=proxy.serve_forever, daemon=True).start()
try:
    with tempfile.TemporaryDirectory(prefix='asura-25-agy-') as directory:
        root = Path(directory)
        workspace = root / 'workspace'
        workspace.mkdir()
        prompt = 'Reply with exactly PROBE_OK'
        if options.case == 'partial':
            prompt = 'Begin with PARTIAL_START on its own line. Then write 1000 numbered lines, each containing a distinct short sentence about trees. Do not use tools or summarize; output every line.'
        if options.case == 'write':
            prompt = 'Create marker.txt in the current directory containing exactly PROBE_OK. Actually write it and report the result.'
        if options.case in ('delayed-control', 'cancel'):
            (workspace / 'delayed.py').write_text(
                "import os, subprocess\nfrom pathlib import Path\n"
                "child = subprocess.Popen(['/usr/bin/python3', '-c', \"import time; from pathlib import Path; time.sleep(8); Path('delayed.txt').write_text('DELAYED')\"])\n"
                "Path('child.pid').write_text(str(child.pid))\nPath('ready').write_text(str(os.getpid()))\nchild.wait()\n"
            )
            prompt = 'Run exactly /usr/bin/python3 delayed.py in the current directory using the terminal tool. Wait for completion then reply DONE. Do not modify the command or files.'
        if options.case in ('side-effect-timeout', 'proxy-failure'):
            (workspace / 'side_effect.py').write_text(
                "import time\nfrom pathlib import Path\np = Path('side_effect.txt')\n"
                "p.write_text(str(int(p.read_text()) + 1 if p.exists() else 1))\nPath('ready').write_text('READY')\nprint('SIDE_EFFECT_OK', flush=True)\ntime.sleep(30)\n"
            )
            prompt = 'Run exactly /usr/bin/python3 side_effect.py once using the terminal tool. Wait for it to finish. Do not modify files or repeat the command.'
        print_timeout = {'short-timeout': '1ms', 'partial': '20s', 'side-effect-timeout': '12s'}.get(options.case, '60s')
        cli_args = [str(cli), '--mode', 'accept-edits', '--model', 'gemini-3.8-flash-low',
                    '--disable-slash-commands', '--dangerously-skip-permissions',
                    '--output-format', 'stream-json', '--print-timeout', print_timeout, '--print=' + prompt]
        args = ['bwrap', '--unshare-pid', '--ro-bind', '/', '/', '--dev', '/dev',
                '--proc', '/proc', '--tmpfs', str(state), '--ro-bind-data', str(fd),
                str(state / 'antigravity-oauth-token'), '--bind', str(root), str(root)]
        if options.isolated_ipc:
            mount_index = args.index('--tmpfs')
            args[mount_index:mount_index] = ['--unshare-ipc', '--tmpfs', '/tmp', '--tmpfs', '/var/tmp', '--tmpfs', '/run/user']
        if options.isolated_home:
            mount_index = args.index('--tmpfs')
            args[mount_index:mount_index] = ['--tmpfs', str(Path.home())]
        if options.case == 'auth-missing':
            index = args.index('--ro-bind-data')
            del args[index:index + 3]
        if options.isolated_home or options.isolated_ipc:
            args += ['--dir', str(cli.parent), '--ro-bind', str(cli), str(cli)]
        if options.readonly_workspace:
            args += ['--ro-bind', str(workspace), str(workspace)]
        args += ['--die-with-parent', '--chdir', str(workspace), '--clearenv',
                 '--setenv', 'HOME', str(Path.home()), '--setenv', 'PATH', '/usr/bin:/bin', '--', *cli_args]
        if proxy:
            index = args.index('--', args.index('--clearenv'))
            url = f'http://127.0.0.1:{proxy.server_port}'
            args[index:index] = ['--setenv', 'HTTPS_PROXY', url, '--setenv', 'https_proxy', url]
        process = subprocess.Popen(args, pass_fds=(fd,), stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        proxy_fault = None
        if options.case == 'proxy-failure':
            deadline = time.monotonic() + 65
            while process.poll() is None and not (workspace / 'ready').exists() and time.monotonic() < deadline:
                time.sleep(0.1)
            connected = bool(proxy_connections)
            ready = (workspace / 'ready').exists()
            proxy_fault = {'ready': ready, 'proxy_used': connected, 'injected': ready and connected}
            if ready and connected:
                proxy_failed.set()
                for pair in proxy_sockets:
                    for sock in pair:
                        try:
                            sock.shutdown(socket.SHUT_RDWR)
                        except OSError:
                            pass
        cancellation = None
        if options.case == 'cancel':
            deadline = time.monotonic() + 65
            while process.poll() is None and not (workspace / 'ready').exists() and time.monotonic() < deadline:
                time.sleep(0.1)
            if (workspace / 'ready').exists():
                ready_age = time.time() - (workspace / 'ready').stat().st_mtime
                before = (workspace / 'delayed.txt').exists()
                tree = []
                for stat in Path('/proc').glob('[0-9]*/stat'):
                    try:
                        fields = stat.read_text().rsplit(')', 1)[1].split()
                        tree.append({'pid': int(stat.parent.name), 'ppid': int(fields[1]), 'pgid': int(fields[2]), 'state': fields[0]})
                    except (FileNotFoundError, ProcessLookupError, PermissionError):
                        pass
                descendants = {process.pid}
                while True:
                    expanded = descendants | {entry['pid'] for entry in tree if entry['ppid'] in descendants}
                    if expanded == descendants:
                        break
                    descendants = expanded
                processes_before = [entry for entry in tree if entry['pid'] in descendants]
                child_namespace_pid = int((workspace / 'child.pid').read_text())
                child_stat = None
                for entry in processes_before:
                    status = Path(f"/proc/{entry['pid']}/status").read_text()
                    namespace_pid = next(int(line.split()[-1]) for line in status.splitlines() if line.startswith('NSpid:'))
                    if namespace_pid == child_namespace_pid:
                        child_stat = Path(f"/proc/{entry['pid']}/stat")
                if child_stat is None:
                    raise RuntimeError('Delayed child absent from descendant snapshot')
                identity = child_stat.read_text().rsplit(')', 1)[1].split()[19]
                os.killpg(process.pid, signal.SIGKILL)
                time.sleep(10)
                current = child_stat.read_text().rsplit(')', 1)[1].split() if child_stat.exists() else None
                child_alive = current is not None and current[19] == identity and current[0] != 'Z'
                cancellation = {'ready': True, 'processes_before_kill': processes_before,
                                'child_alive_after_10s': child_alive, 'ready_age_seconds': round(ready_age, 3),
                                'marker_before_kill': before, 'observation_seconds': 10,
                                'delayed_marker_after': (workspace / 'delayed.txt').read_text() if (workspace / 'delayed.txt').exists() else None}
            else:
                cancellation = {'ready': False}
        timed_out = False
        try:
            stdout, stderr = process.communicate(timeout=75)
        except subprocess.TimeoutExpired:
            timed_out = True
            os.killpg(process.pid, signal.SIGKILL)
            stdout, stderr = process.communicate()
        # Reject output containing the full token or long credential fields.
        sensitive = [token.strip(), original_token.strip()]
        # 2026-09-30：keyring 的 access／refresh 位於巢狀 token，兩份來源都要掃描。
        def collect_sensitive(value):
            if isinstance(value, dict):
                for item in value.values():
                    collect_sensitive(item)
            elif isinstance(value, list):
                for item in value:
                    collect_sensitive(item)
            elif isinstance(value, str) and len(value) > 20:
                sensitive.append(value.encode())
        for payload in (token, original_token):
            collect_sensitive(json.loads(payload))
        if any(v and (v in stdout or v in stderr) for v in sensitive):
            raise SystemExit('Credential in output; refusing to save evidence')
        events = []
        for line in stdout.decode(errors='replace').splitlines():
            try:
                events.append(json.loads(line))
            except ValueError:
                events.append({'non_json': line})
        print(json.dumps({'date': datetime.date.today().isoformat(), 'case': options.case,
                          'readonly_workspace': options.readonly_workspace, 'isolated_home': options.isolated_home, 'isolated_ipc': options.isolated_ipc, 'pid_namespace': True, 'access_only': options.access_only, 'auth_source': options.auth_source,
                          'version': subprocess.check_output([str(cli), '--version'], text=True).strip(),
                          'cli_sha256': cli_sha, 'script_sha256': script_sha,
                          'argv': cli_args, 'exit_code': process.returncode, 'timed_out': timed_out,
                          'marker': (workspace / 'marker.txt').read_text() if (workspace / 'marker.txt').exists() else None,
                          'delayed_marker': (workspace / 'delayed.txt').read_text() if (workspace / 'delayed.txt').exists() else None,
                          'cancellation': cancellation, 'events': events, 'proxy_fault': proxy_fault, 'proxy_connections': proxy_connections,
                          'side_effect_count': int((workspace / 'side_effect.txt').read_text()) if (workspace / 'side_effect.txt').exists() else 0,
                          'stderr': stderr.decode(errors='replace')}, ensure_ascii=False, indent=2))
finally:
    os.close(fd)
    if proxy:
        proxy_failed.set()
        proxy.shutdown()
        proxy.server_close()
