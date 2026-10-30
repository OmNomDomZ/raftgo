#!/usr/bin/env python3
"""Manual test dashboard: one page with /status of every node and Pause/Resume buttons.
Won elections (with vote counts) are taken from the nodes' "became leader" log lines.

    python3 scenarios/dashboard.py           # Docker stand from scenarios/compose.yaml
    python3 scenarios/dashboard.py --local   # five local processes, no Docker

Python standard library only. Does not change the stand: Docker mode runs the same
`docker compose -p raftgo-scenarios ...` commands as run.py and leaves containers
running on exit. Do not run failure scenarios concurrently with manual controls.
Failures use pause/unpause (SIGSTOP/SIGCONT locally), preserving RAM.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import threading
import time
import urllib.request
import webbrowser

ROOT = Path(__file__).resolve().parent
REPO = ROOT.parent
COMPOSE = ['docker', 'compose', '-p', 'raftgo-scenarios', '-f', str(ROOT / 'compose.yaml')]
NODES = [f'node{i}' for i in range(1, 6)]
API = {n: f'http://127.0.0.1:{18000 + i}' for i, n in enumerate(NODES, 1)}
RPC = {n: f'http://127.0.0.1:{19000 + i}' for i, n in enumerate(NODES, 1)}


class DockerNodes:
    def __init__(self):
        subprocess.run(COMPOSE + ['up', '-d', '--build'], check=True)
        self.since = datetime.now(timezone.utc).isoformat()

    def log_lines(self):
        """Log lines written since the previous call; may repeat a few, the caller dedupes."""
        since, self.since = self.since, datetime.now(timezone.utc).isoformat()
        out = subprocess.run(COMPOSE + ['logs', '--no-color', '--no-log-prefix', '--since', since],
                             capture_output=True, text=True)
        return out.stdout.splitlines()

    def stop(self, node):
        subprocess.run(COMPOSE + ['pause', node], check=True)

    def start(self, node):
        subprocess.run(COMPOSE + ['unpause', node], check=True)

    def close(self):
        print('Контейнеры оставлены запущенными. Остановить: '
              'docker compose -p raftgo-scenarios -f scenarios/compose.yaml down')


class LocalNodes:
    def __init__(self):
        for url in list(API.values()) + list(RPC.values()):
            port = int(url.rsplit(':', 1)[1])
            with socket.socket() as s:
                if s.connect_ex(('127.0.0.1', port)) == 0:
                    raise SystemExit(f'Порт {port} занят. Запущен Docker-стенд? Остановите его '
                                     '(docker compose -p raftgo-scenarios -f scenarios/compose.yaml down) '
                                     'или запускайте dashboard без --local.')
        self.work = Path(tempfile.mkdtemp(prefix='raftgo-dashboard-'))
        self.binary = self.work / 'raftgo'
        subprocess.run(['go', 'build', '-o', str(self.binary), './cmd/raftgo'], cwd=REPO, check=True)
        members = [{'id': n, 'rpc_url': RPC[n], 'api_url': API[n]} for n in NODES]
        for n in NODES:
            cfg = {'listen': API[n][len('http://'):], 'rpc_listen': RPC[n][len('http://'):],
                   'members': members}
            (self.work / f'{n}.json').write_text(json.dumps(cfg))
        self.procs = {}
        self.offsets = {n: 0 for n in NODES}
        self.lock = threading.Lock()
        for n in NODES:
            self.start(n)
        print(f'Конфиги и логи нод: {self.work}; состояние кластера только в RAM')

    def start(self, node):
        with self.lock:
            if node in self.procs:
                proc = self.procs[node]
                if proc.poll() is not None:
                    raise RuntimeError('Процесс завершён; пересоздайте весь кластер')
                proc.send_signal(signal.SIGCONT)
                return
            log = open(self.work / f'{node}.log', 'a')
            self.procs[node] = subprocess.Popen(
                [str(self.binary), '-config', str(self.work / f'{node}.json'), '-id', node,
                 '-log-level', 'debug'], stdout=log, stderr=log, start_new_session=True)
            log.close()

    def stop(self, node):
        with self.lock:
            proc = self.procs.get(node)
            if proc is not None:
                if proc.poll() is not None:
                    raise RuntimeError('Процесс завершён; пересоздайте весь кластер')
                proc.send_signal(signal.SIGSTOP)

    def log_lines(self):
        lines = []
        for n in NODES:
            try:
                with open(self.work / f'{n}.log', 'rb') as f:
                    f.seek(self.offsets[n])
                    data = f.read()
            except OSError:
                continue
            data = data[:data.rfind(b'\n') + 1]  # keep a half-written line for the next call
            self.offsets[n] += len(data)
            lines += data.decode(errors='replace').splitlines()
        return lines

    def close(self):
        for proc in self.procs.values():
            if proc.poll() is None:
                proc.send_signal(signal.SIGCONT)
                proc.terminate()
        for proc in self.procs.values():
            try:
                proc.wait(15)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()


def status(node):
    try:
        with urllib.request.urlopen(API[node] + '/status', timeout=0.7) as r:
            return {'id': node, 'up': True, **json.load(r)['node']}
    except (OSError, ValueError, KeyError):
        return {'id': node, 'up': False}


class Elections:
    """Collects "became leader" events from node logs in a background thread."""

    def __init__(self):
        self.events = []
        self.seen = set()
        self.lock = threading.Lock()

    def watch(self, nodes):
        while True:
            try:
                lines = nodes[0].log_lines() if nodes else []
            except Exception as e:
                print(f'logs: {e}')
                lines = []
            for line in lines:
                if '"became leader"' not in line:
                    continue
                try:
                    rec = json.loads(line)
                except ValueError:
                    continue
                key = (rec.get('node'), rec.get('time'))
                with self.lock:
                    if key in self.seen:
                        continue
                    self.seen.add(key)
                    self.events.append({'seq': len(self.events) + 1, 'node': rec.get('node'),
                                        'term': rec.get('term'), 'votes': rec.get('votes')})
            time.sleep(1)

    def list(self):
        with self.lock:
            return self.events[-100:]


def make_handler(nodes, pool, elections):
    busy = set()

    def control(action, node):
        try:
            getattr(nodes[0], action)(node)
        except Exception as e:  # shown in the terminal; the page sees the node state anyway
            print(f'{action} {node}: {e}')
        finally:
            busy.discard(node)

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def reply(self, code, body, ctype='application/json'):
            data = body.encode()
            self.send_response(code)
            self.send_header('Content-Type', ctype + '; charset=utf-8')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            if self.path == '/':
                self.reply(200, PAGE, 'text/html')
            elif self.path == '/api/status':
                rows = list(pool.map(status, NODES))
                for row in rows:
                    row['busy'] = row['id'] in busy
                self.reply(200, json.dumps({'nodes': rows, 'elections': elections.list()}))
            else:
                self.reply(404, '{}')

        def do_POST(self):
            parts = self.path.strip('/').split('/')  # api/<stop|start>/<node>
            if len(parts) != 3 or parts[0] != 'api' or parts[1] not in ('stop', 'start') or parts[2] not in NODES:
                self.reply(404, '{}')
                return
            node = parts[2]
            if node not in busy:
                busy.add(node)
                threading.Thread(target=control, args=(parts[1], node), daemon=True).start()
            self.reply(202, '{}')

    return Handler


PAGE = r'''<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>raftgo dashboard</title>
<style>
:root { --bg:#f5f5f4; --card:#fff; --fg:#1c1917; --muted:#78716c; --line:#e7e5e4;
        --leader:#16a34a; --cand:#d97706; --down:#a8a29e; --btn:#f5f5f4; }
@media (prefers-color-scheme: dark) {
  :root { --bg:#1c1917; --card:#292524; --fg:#f5f5f4; --muted:#a8a29e; --line:#44403c;
          --leader:#4ade80; --cand:#fbbf24; --down:#57534e; --btn:#3a3532; }
}
* { box-sizing: border-box; }
body { margin:0; padding:20px 16px; background:var(--bg); color:var(--fg);
       font:14px/1.4 -apple-system, system-ui, sans-serif; }
h1 { font-size:18px; margin:0 0 4px; }
.sub { color:var(--muted); margin-bottom:16px; }
.grid { display:grid; grid-template-columns:repeat(auto-fill, minmax(190px, 1fr)); gap:12px; }
.card { background:var(--card); border:2px solid var(--line); border-radius:10px; padding:12px; }
.card.Leader { border-color:var(--leader); }
.card.Candidate { border-color:var(--cand); }
.card.down { opacity:.55; }
.head { display:flex; justify-content:space-between; align-items:baseline; margin-bottom:8px; }
.name { font-weight:600; font-size:16px; }
.role { font-weight:600; }
.Leader .role { color:var(--leader); } .Candidate .role { color:var(--cand); } .down .role { color:var(--down); }
dl { display:grid; grid-template-columns:auto 1fr; gap:2px 10px; margin:0 0 10px; font-variant-numeric:tabular-nums; }
dt { color:var(--muted); } dd { margin:0; }
.btns { display:flex; gap:8px; }
button { flex:1; padding:6px; border:1px solid var(--line); border-radius:6px;
         background:var(--btn); color:var(--fg); font:inherit; cursor:pointer; }
button:disabled { opacity:.4; cursor:default; }
.bar { display:flex; gap:8px; margin-bottom:12px; flex-wrap:wrap; }
.bar button { flex:0 0 auto; padding:6px 12px; }
h2 { font-size:15px; margin:20px 0 6px; }
#events { background:var(--card); border:1px solid var(--line); border-radius:10px; padding:8px 12px;
          max-height:260px; overflow:auto; font:12px/1.5 ui-monospace, Menlo, monospace; }
#events div { white-space:pre-wrap; }
#events .win { color:var(--leader); font-weight:600; }
</style>
</head>
<body>
<h1>raftgo: 5 нод</h1>
<div class="sub" id="sub">Опрос /status раз в секунду</div>
<div class="bar">
  <button onclick="act('stop', leader())">Pause лидера</button>
  <button onclick="NODES.forEach(n => act('start', n))">Resume все</button>
</div>
<div class="grid" id="grid"></div>
<h2>События</h2>
<div id="events"></div>
<script>
const NODES = ['node1','node2','node3','node4','node5'];
let last = {}, lastSeq = 0, quorum = 3;
const esc = s => String(s ?? '').replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
const leader = () => Object.values(last).find(s => s.up && s.role === 'Leader')?.id;

function event(text, cls) {
  const box = document.getElementById('events');
  const line = document.createElement('div');
  if (cls) line.className = cls;
  line.textContent = new Date().toLocaleTimeString() + '  ' + text;
  box.prepend(line);
}

async function act(action, node) {
  if (!node) return;
  event(`${action} ${node}`);
  await fetch(`/api/${action}/${node}`, {method: 'POST'});
  refresh();
}

function card(s) {
  const role = s.up ? s.role : 'down';
  const rows = s.up ? [
    ['term', s.term], ['leader', s.leader_id || '-'], ['voted_for', s.voted_for || '-'],
    ['votes', `${s.votes} / quorum ${s.quorum}`], ['commit', s.commit_index], ['last_log', s.last_log_index],
  ] : [['', s.busy ? 'выполняется…' : 'не отвечает']];
  return `<div class="card ${esc(s.up ? s.role : 'down')}">
    <div class="head"><span class="name">${esc(s.id)}</span><span class="role">${esc(role)}</span></div>
    <dl>${rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join('')}</dl>
    <div class="btns">
      <button ${!s.up || s.busy ? 'disabled' : ''} onclick="act('stop','${s.id}')">Pause</button>
      <button ${s.up || s.busy ? 'disabled' : ''} onclick="act('start','${s.id}')">Resume</button>
    </div></div>`;
}

function diff(prev, s) {
  if (!prev) return;
  if (prev.up && !s.up) event(`${s.id}: остановлена`);
  if (!prev.up && s.up) event(`${s.id}: поднялась (${s.role}, term ${s.term})`);
  if (prev.up && s.up && (prev.role !== s.role || prev.term !== s.term))
    event(`${s.id}: ${prev.role} term ${prev.term} → ${s.role} term ${s.term}`);
}

async function refresh() {
  try {
    const {nodes: rows, elections} = await (await fetch('/api/status')).json();
    rows.forEach(s => diff(last[s.id], s));
    quorum = rows.find(s => s.up)?.quorum ?? quorum;
    elections.filter(e => e.seq > lastSeq).forEach(e =>
      event(`ВЫБОРЫ: ${e.node} стала Leader в term ${e.term}, голосов ${e.votes} из 5 (нужно ${quorum})`, 'win'));
    if (elections.length) lastSeq = elections[elections.length - 1].seq;
    last = Object.fromEntries(rows.map(s => [s.id, s]));
    document.getElementById('grid').innerHTML = rows.map(card).join('');
    const up = rows.filter(s => s.up), leaders = up.filter(s => s.role === 'Leader');
    document.getElementById('sub').textContent =
      `Работают ${up.length} из 5 · лидеров: ${leaders.length}` +
      (leaders.length === 1 ? ` (${leaders[0].id}, term ${leaders[0].term})` : '');
  } catch (e) {
    document.getElementById('sub').textContent = 'dashboard.py не отвечает';
  }
}
refresh();
setInterval(refresh, 1000);
</script>
</body>
</html>
'''


def interrupt(*_):
    raise KeyboardInterrupt  # SIGTERM cleans up like Ctrl+C


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--local', action='store_true', help='run five local processes instead of Docker')
    parser.add_argument('--port', type=int, default=8090, help='dashboard port (default 8090)')
    args = parser.parse_args()

    holder = []  # the handler needs nodes, but bind the port first so a busy port fails before nodes start
    elections = Elections()
    server = ThreadingHTTPServer(('127.0.0.1', args.port),
                                 make_handler(holder, ThreadPoolExecutor(len(NODES)), elections))
    signal.signal(signal.SIGTERM, interrupt)
    nodes = LocalNodes() if args.local else DockerNodes()
    holder.append(nodes)
    threading.Thread(target=elections.watch, args=(holder,), daemon=True).start()
    url = f'http://127.0.0.1:{args.port}'
    print(f'Dashboard: {url}  (Ctrl+C для выхода)')
    if not os.environ.get('NO_BROWSER'):
        webbrowser.open(url)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
        nodes.close()


if __name__ == '__main__':
    main()
