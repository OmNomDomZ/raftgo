#!/usr/bin/env python3
"""Acceptance scenarios; Python standard library only. Run from the host."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent
COMPOSE = ['docker', 'compose', '-p', 'raftgo-scenarios', '-f', str(ROOT / 'compose.yaml')]
NODES = [f'node{i}' for i in range(1, 6)]
PAUSED = set()
BASE = {n: f'http://127.0.0.1:{18000 + i}' for i, n in enumerate(NODES, 1)}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def step(message):
    print(f'\n>>> {message}', flush=True)


def compose(*args):
    # Track before Docker runs: a partially failed pause also needs cleanup.
    if args[0] == 'pause':
        PAUSED.update(args[1:] or NODES)
    subprocess.run(COMPOSE + list(args), check=True, timeout=45)
    if args[0] == 'unpause':
        PAUSED.difference_update(args[1:] or NODES)


def request(node, method, path, data=None, expected=None, timeout=8):
    headers = {'Content-Type': 'application/json'}
    req = urllib.request.Request(BASE[node] + path, method=method, headers=headers,
                                 data=None if data is None else json.dumps(data).encode())
    try:
        response = urllib.request.build_opener(NoRedirect).open(req, timeout=timeout)
    except urllib.error.HTTPError as err:
        response = err
    with response:
        result = json.load(response)
        code, location = response.status, response.headers.get('Location')
    if expected is not None:
        check(code == expected, f'{node} {method} {path}: expected {expected}, got {code}: {result}')
        print(f'  {node} {method} {path} -> {code} {json.dumps(result, ensure_ascii=False)}', flush=True)
    return code, result, location


def eventually(fn, timeout=30):
    deadline = time.monotonic() + timeout
    last = None
    print(f'  Waiting up to {timeout}s for condition', flush=True)
    while time.monotonic() < deadline:
        try:
            value = fn()
            if value:
                return value
        except (AssertionError, OSError, urllib.error.URLError) as err:
            last = err
        time.sleep(0.2)
    raise AssertionError(f'Condition not met within {timeout}s; last error: {last}')


def status(node):
    return request(node, 'GET', '/status', timeout=0.7)[1]['node']


def leader(nodes=NODES):
    def ready():
        states = {n: status(n) for n in nodes if n not in PAUSED}
        if not states:
            return False
        leaders = [n for n, s in states.items() if s['role'] == 'Leader']
        if len(leaders) != 1:
            return False
        chosen = leaders[0]
        if not all(s['leader_id'] == chosen and s['term'] == states[chosen]['term'] for s in states.values()):
            return False
        return chosen
    return eventually(ready)


def failover(old):
    compose('pause', old)
    new = leader([n for n in NODES if n != old])
    check(new != old, 'Leader did not change')
    print(f'  New leader: {new}', flush=True)
    return new


def task1():
    step('1.1 All five nodes agree on one leader and term')
    current = leader()
    term = status(current)['term']
    for n in NODES:
        _, body, _ = request(n, 'GET', '/election', expected=200)
        s = body['node']
        check(s['id'] == n and s['running'] and s['quorum'] == 3, 'Invalid node status')
        check(s['role'] == ('Leader' if n == current else 'Follower'), 'Unexpected role')
        check(s['term'] == term and s['leader_id'] == current, 'Election disagreement')
    step('1.2 Pause leader; four nodes elect a leader in a higher term')
    new = failover(current)
    check(status(new)['term'] > term, 'Term did not increase')
    step('1.3 Unpause old leader; it joins as a follower')
    compose('unpause', current)
    leader()
    check(status(current)['role'] == 'Follower', 'Old leader did not step down')
    step('1.4 Pause three nodes including leader; two cannot elect a leader')
    current = leader()
    paused = [current] + [n for n in NODES if n != current][:2]
    compose('pause', *paused)
    survivors = [n for n in NODES if n not in paused]
    # Several full election timeout windows; this is an observation, not a proof.
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        check(all(status(n)['role'] != 'Leader' for n in survivors), 'Minority elected a leader')
        time.sleep(0.2)
    compose('unpause', *paused)
    print(f'  Resumed leader: {leader()}')

    step('1.5 Pause/unpause all processes retains terms, votes and container processes')
    leader()
    before = {n: status(n) for n in NODES}
    def processes():
        ids = subprocess.check_output(COMPOSE + ['ps', '-q'], text=True, timeout=15).split()
        check(len(ids) == 5, 'Expected five containers')
        return json.loads(subprocess.check_output(
            ['docker', 'inspect', *ids], text=True, timeout=15))
    saved = {c['Id']: (c['State']['Pid'], c['State']['StartedAt']) for c in processes()}
    compose('pause')
    time.sleep(0.25)
    compose('unpause')
    after = {n: status(n) for n in NODES}
    check(saved == {c['Id']: (c['State']['Pid'], c['State']['StartedAt']) for c in processes()},
          'A container process was replaced')
    for n, state in after.items():
        check(state['term'] >= before[n]['term'], f'{n}: term was lost')
        if state['term'] == before[n]['term']:
            check(state['voted_for'] == before[n]['voted_for'], f'{n}: vote changed in same term')
    print('  Same container IDs, PIDs and StartedAt; terms retained, same-term votes unchanged')
    leader()


def cleanup():
    # Query Docker, not just PAUSED: a failed pause may have completed partially.
    result = subprocess.run(COMPOSE + ['ps', '--status', 'paused', '--services'],
                            check=True, capture_output=True, text=True, timeout=15)
    errors = []
    for node in result.stdout.split():
        if node not in NODES:
            continue
        try:
            compose('unpause', node)
        except (subprocess.SubprocessError, OSError) as err:
            errors.append(f'{node}: {err}')
    if errors:
        raise RuntimeError('Unpause failed: ' + '; '.join(errors))


def diagnostics():
    def inspect(node):
        try:
            return node, status(node)
        except (OSError, urllib.error.URLError, ValueError) as err:
            return node, str(err)
    print("\nFailure diagnostics (independent local observations):", flush=True)
    with ThreadPoolExecutor(max_workers=5) as pool:
        for node, result in pool.map(inspect, NODES):
            print(f"  {node}: {result}", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('task', choices=['1', 'all'])
    args = parser.parse_args()
    try:
        for number in ('1' if args.task == 'all' else args.task):
            globals()['task' + number]()
            print(f'\nPASS task{number}', flush=True)
    except Exception:
        diagnostics()
        raise
    finally:
        # Unpause all nodes, including a partially completed Docker pause.
        cleanup()


if __name__ == '__main__':
    main()
