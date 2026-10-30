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
import uuid

ROOT = Path(__file__).resolve().parent
COMPOSE = ['docker', 'compose', '-p', 'raftgo-scenarios', '-f', str(ROOT / 'compose.yaml')]
NODES = [f'node{i}' for i in range(1, 6)]
PAUSED = set()
DISCONNECTED = {}
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


def disconnect(node):
    # Pausing alone queues TCP data: on resume it can deliver the orphan before
    # elections. Detach three paused replicas before proposing the tail instead.
    container = subprocess.check_output(COMPOSE + ['ps', '-q', node], text=True, timeout=15).strip()
    details = json.loads(subprocess.check_output(['docker', 'inspect', container], text=True, timeout=15))[0]
    networks = details['NetworkSettings']['Networks']
    check(len(networks) == 1, f'Expected one scenario network for {node}')
    network, endpoint = next(iter(networks.items()))
    check(network == 'raftgo-scenarios_default', f'Unexpected network: {network}')
    DISCONNECTED[node] = (container, network, endpoint['IPAddress'], endpoint['Aliases'])
    subprocess.run(['docker', 'network', 'disconnect', network, container], check=True, timeout=15)
    print(f'  Network isolated: {node}', flush=True)


def reconnect(node):
    container, network, address, aliases = DISCONNECTED[node]
    networks = json.loads(subprocess.check_output(['docker', 'inspect', container], text=True, timeout=15))[0]['NetworkSettings']['Networks']
    if network not in networks:
        command = ['docker', 'network', 'connect', '--ip', address]
        for alias in aliases or []:
            command += ['--alias', alias]
        subprocess.run(command + [network, container], check=True, timeout=15)
    del DISCONNECTED[node]
    print(f'  Network restored: {node}', flush=True)


def request(node, method, path, data=None, token=None, expected=None, timeout=8):
    headers = {'Content-Type': 'application/json'}
    if token is not None:
        headers['X-Lock-Token'] = token
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


def follower(current):
    return next(n for n in NODES if n != current)


def redirect(node, method, path, data, target):
    code, _, location = request(node, method, path, data, expected=307)
    check(location == BASE[target] + path, f'Wrong Location: {location}')


def logs_match(index, nodes=NODES, expected_prefix=None):
    def ready():
        logs = [request(n, 'GET', '/log')[1] for n in nodes]
        if any(l['node']['commit_index'] < index for l in logs):
            return False
        prefix = logs[0]['entries'][:index]
        check(len(prefix) == index, 'Missing committed entries')
        if expected_prefix is not None:
            check(prefix[:len(expected_prefix)] == expected_prefix, 'Previously committed prefix changed')
        return all(l['entries'][:index] == prefix for l in logs)
    eventually(ready)
    print(f'  Committed prefix through index {index} matches on {list(nodes)}', flush=True)


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


def task2():
    step('2.1 add: follower redirects POST /entries, leader commits it')
    current = leader()
    command = 'demo-add-' + uuid.uuid4().hex
    redirect(follower(current), 'POST', '/entries', {'command': command}, current)
    _, entry, _ = request(current, 'POST', '/entries', {'command': command}, expected=201)
    check(entry['committed'], 'Write was not confirmed')
    logs_match(entry['index'])
    for n in NODES:
        item = request(n, 'GET', '/log')[1]['entries'][entry['index'] - 1]
        check(item['command'] == command and item['term'] == entry['term'], 'Incorrect entry')
    step('2.2 Offline follower catches up after two further adds')
    offline = follower(current)
    compose('pause', offline)
    for suffix in ('-2', '-3'):
        _, last, _ = request(current, 'POST', '/entries', {'command': command + suffix}, expected=201)
    compose('unpause', offline)
    leader()
    logs_match(last['index'])
    step('2.3 Committed prefix survives leader failure')
    current = leader()
    saved = request(current, 'GET', '/log')[1]['entries'][:last['index']]
    new = failover(current)
    _, last, _ = request(new, 'POST', '/entries', {'command': command + '-new-leader'}, expected=201)
    compose('unpause', current)
    leader()
    logs_match(last['index'], expected_prefix=saved)
    step('2.4 No quorum: add must not return success')
    current = leader()
    paused = [n for n in NODES if n != current][:3]
    compose('pause', *paused)
    code, body, _ = request(current, 'POST', '/entries', {'command': command + '-uncertain'})
    check(code in (503, 504), f'Minority acknowledged write: {code} {body}')
    print(f'  add without quorum -> {code}; outcome may be unknown')
    compose('unpause', *paused)
    leader()

    step('2.5 Conflicting uncommitted tail is replaced after leader change')
    current = leader()
    # Freeze all followers before proposing: only the old leader has the tail.
    # Do not restart any process; term, vote and log remain in RAM.
    def committed_baseline():
        entries = request(current, 'GET', '/log')[1]['entries']
        return entries if entries and all(e['committed'] for e in entries) else None
    saved = eventually(committed_baseline)
    others = [n for n in NODES if n != current]
    compose('pause', *others)
    majority = others[:3]
    for n in majority:
        disconnect(n)
    orphan = command + '-orphan'
    with ThreadPoolExecutor(max_workers=1) as pool:
        pending = pool.submit(request, current, 'POST', '/entries', {'command': orphan}, timeout=40)
        try:
            def tail_visible():
                entries = request(current, 'GET', '/status', timeout=0.7)[1]['entries']
                return next((e for e in entries if e['command'] == orphan and not e['committed']), None)
            tail = eventually(tail_visible, timeout=3)
            print(f"  Old leader {current}: uncommitted index={tail['index']} term={tail['term']}", flush=True)
            compose('pause', current)
            for n in majority:
                reconnect(n)
            compose('unpause', *majority)
            new = leader(majority)
            _, replacement, _ = request(new, 'POST', '/entries', {'command': command + '-replacement'}, expected=201)
            check(replacement['term'] > tail['term'], 'Replacement must belong to a newer term')
            logs_match(replacement['index'], majority, expected_prefix=saved)
        finally:
            # Also release the suspended request handler if setup/checks fail.
            cleanup()
        code, body, _ = pending.result(timeout=40)
        check(code in (409, 504), f'Orphan acknowledged: {code} {body}')
        print(f'  Old request -> {code}; never success', flush=True)
    leader()
    logs_match(replacement['index'], expected_prefix=saved)
    for n in NODES:
        entries = request(n, 'GET', '/status')[1]['entries']
        check(all(e['command'] != orphan for e in entries), f'Orphan remains on {n}')
        repaired = entries[tail['index'] - 1]
        check(repaired['term'] > tail['term'] and repaired['committed'], f'Tail not replaced on {n}')
    print('  Conflicting tail removed on all five replicas; committed prefix preserved', flush=True)


def task3():
    step('3.1 CRUD without locks, duplicate create and missing key')
    current = leader()
    path = '/kv/crud-' + uuid.uuid4().hex
    redirect(follower(current), 'POST', path, {'value': 'first'}, current)
    request(current, 'GET', path, expected=404)
    request(current, 'POST', path, {'value': 'first'}, expected=201)
    request(current, 'POST', path, {'value': 'wrong'}, expected=409)
    check(request(current, 'GET', path, expected=200)[1]['value'] == 'first', 'Create overwritten')
    value = 'строка с JSON: {"answer":42}\nsecond line'
    request(current, 'PUT', path, {'value': value}, expected=200)
    check(request(current, 'GET', path, expected=200)[1]['value'] == value, 'Wrong updated value')
    request(current, 'PUT', path, {'value': ''}, expected=200)
    check(request(current, 'GET', path, expected=200)[1]['value'] == '', 'Empty string lost')
    request(current, 'PUT', path, {'value': value}, expected=200)
    step('3.2 Value survives a new leader and pause/unpause of all processes (RAM retained)')
    new = failover(current)
    check(request(new, 'GET', path, expected=200)[1]['value'] == value, 'Failover lost value')
    compose('unpause', current)
    leader()
    compose('pause')
    compose('unpause')
    current = leader()
    check(request(current, 'GET', path, expected=200)[1]['value'] == value, 'Pause lost value')
    step('3.3 Delete and PUT upsert')
    request(current, 'DELETE', path, expected=200)
    request(current, 'GET', path, expected=404)
    request(current, 'DELETE', path, expected=404)
    request(current, 'PUT', path, {'value': 'upsert'}, expected=200)
    check(request(current, 'GET', path, expected=200)[1]['value'] == 'upsert', 'Upsert failed')
    step('3.4 Without quorum neither write nor linearizable read succeeds')
    paused = [n for n in NODES if n != current][:3]
    compose('pause', *paused)
    for method, data in [('PUT', {'value': 'uncertain'}), ('GET', None)]:
        code, body, _ = request(current, method, path, data)
        check(code in (503, 504), f'Minority served {method}: {code} {body}')
        print(f'  {method} without quorum -> {code}')
    compose('unpause', *paused)
    leader()


def task4():
    step('4.1 CAS rejects wrong expected value without changing data')
    current = leader()
    path = '/kv/cas-' + uuid.uuid4().hex
    request(current, 'POST', path, {'value': '0'}, expected=201)
    request(current, 'POST', path + '/cas', {'expected': 'wrong', 'value': 'bad'}, expected=409)
    check(request(current, 'GET', path, expected=200)[1]['value'] == '0', 'Failed CAS changed data')
    step('4.2 Two concurrent CAS: exactly one winner')
    def cas(value):
        return value, request(current, 'POST', path + '/cas', {'expected': '0', 'value': value})[0]
    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(cas, ['A', 'B']))
    check(sorted(code for _, code in results) == [200, 409], f'CAS results: {results}')
    winner = next(value for value, code in results if code == 200)
    check(request(current, 'GET', path, expected=200)[1]['value'] == winner, 'Wrong CAS winner')
    print(f'  CAS clients: {results}')
    step('4.3 Concurrent lock acquisition: exactly one token')
    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(lambda _: request(current, 'POST', path + '/lock'), range(2)))
    check(sorted(r[0] for r in results) == [201, 423], f'Lock results: {results}')
    token = next(r[1]['token'] for r in results if r[0] == 201)
    for method, suffix, body in [('GET', '', None), ('PUT', '', {'value': 'bad'}),
                                 ('DELETE', '', None), ('POST', '/cas', {'expected': winner, 'value': 'bad'}),
                                 ('DELETE', '/lock', None)]:
        request(current, method, path + suffix, body, expected=423)
    request(current, 'PUT', path, {'value': 'protected'}, token=token, expected=200)
    request(current, 'POST', path + '/cas', {'expected': 'protected', 'value': 'locked-cas'}, token=token, expected=200)
    step('4.4 Lock survives leader failure and pause/unpause of all processes')
    new = failover(current)
    request(new, 'GET', path, expected=423)
    check(request(new, 'GET', path, token=token, expected=200)[1]['value'] == 'locked-cas', 'Locked value lost')
    compose('unpause', current)
    leader()
    compose('pause')
    compose('unpause')
    current = leader()
    request(current, 'GET', path, expected=423)
    request(current, 'GET', path, token=token, expected=200)
    step('4.5 Release, reacquire, reject stale token; CRUD under lock')
    request(current, 'DELETE', path + '/lock', token=token, expected=200)
    request(current, 'PUT', path, {'value': 'stale'}, token=token, expected=409)
    new_token = request(current, 'POST', path + '/lock', expected=201)[1]['token']
    check(int(new_token) > int(token), 'Fence did not increase')
    request(current, 'PUT', path, {'value': 'stale'}, token=token, expected=423)
    request(current, 'DELETE', path + '/lock', token=token, expected=423)
    request(current, 'DELETE', path, token=new_token, expected=200)
    request(current, 'POST', path, {'value': 'without-token'}, expected=423)
    request(current, 'POST', path, {'value': 'recreated'}, token=new_token, expected=201)
    request(current, 'DELETE', path + '/lock', token=new_token, expected=200)
    check(request(current, 'GET', path, expected=200)[1]['value'] == 'recreated', 'Final value wrong')


def cleanup():
    errors = []
    for node in list(DISCONNECTED):
        try:
            reconnect(node)
        except (subprocess.SubprocessError, OSError) as err:
            errors.append(f'network {node}: {err}')
    # Query Docker, not just PAUSED: a failed pause may have completed partially.
    result = subprocess.run(COMPOSE + ['ps', '--status', 'paused', '--services'],
                            check=True, capture_output=True, text=True, timeout=15)
    for node in result.stdout.split():
        if node not in NODES:
            continue
        try:
            compose('unpause', node)
        except (subprocess.SubprocessError, OSError) as err:
            errors.append(f'{node}: {err}')
    if errors:
        raise RuntimeError('Cleanup failed: ' + '; '.join(errors))


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
    parser.add_argument('task', choices=['1', '2', '3', '4', 'all'])
    args = parser.parse_args()
    try:
        for number in ('1234' if args.task == 'all' else args.task):
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
