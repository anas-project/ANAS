# TEST_CASES: LLNG-READY-T-007
"""Isolated same-user OIDC microbenchmark; writes aggregates, never credentials/tokens."""
import concurrent.futures, http.client, http.cookies, json, math, os, pathlib, statistics, subprocess, sys, threading, time, urllib.parse
from html.parser import HTMLParser
P = pathlib.Path('/home/whl/anas-llng-ready-test-20261023')
os.umask(0o077)
S = json.loads((P / 'secrets.json').read_text())
D = ['docker', '-H', 'unix:///run/anas-llng-test-1023.sock']
provider = sys.argv[1]
assert provider in ('llng', 'casdoor')

class Forms(HTMLParser):

    def __init__(self):
        super().__init__()
        self.fields = {}

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == 'input' and a.get('type') == 'hidden' and a.get('name'):
            self.fields[a['name']] = a.get('value', '')

class Client:

    def __init__(self):
        self.cookies = {}

    def request(self, path, body=None, kind='application/x-www-form-urlencoded'):
        c = http.client.HTTPConnection('10.253.26.2', 80 if provider == 'llng' else 8000, timeout=30)
        headers = {'Host': 'auth.llngtest.invalid', 'Accept': 'text/html', 'Cookie': '; '.join((k + '=' + v for k, v in self.cookies.items()))}
        if body is not None:
            headers['Content-Type'] = kind
        c.request('POST' if body is not None else 'GET', path, body, headers)
        r = c.getresponse()
        b = r.read()
        h = dict(((k.lower(), v) for k, v in r.getheaders()))
        for k, v in r.getheaders():
            if k.lower() == 'set-cookie':
                for n, m in http.cookies.SimpleCookie(v).items():
                    self.cookies[n] = m.value
        status = r.status
        c.close()
        return (status, h, b)

    def follow(self, path, body=None):
        for _ in range(12):
            u = urllib.parse.urlsplit(path)
            if u.hostname == 'tasks.llngtest.invalid':
                return urllib.parse.parse_qs(u.query).get('code', [''])[0]
            status, h, b = self.request(u.path + ('?' + u.query if u.query else ''), body)
            body = None
            if 300 <= status < 400:
                path = h['location']
                continue
            if status != 200:
                raise RuntimeError('LLNG authorization HTTP ' + str(status))
            f = Forms()
            f.feed(b.decode())
            f.fields.update(user='llngnormal', password=S['old'], lmAuth='1_Password')
            body = urllib.parse.urlencode(f.fields)
        raise RuntimeError('LLNG authorization did not return a code')

def authenticate(n):
    started = time.monotonic()
    c = Client()
    redirect = 'https://tasks.llngtest.invalid:18443/auth/openid/anas'
    nonce = 'bench-' + str(n) + '-' + str(time.monotonic_ns())
    if provider == 'llng':
        path = '/oauth2/authorize?' + urllib.parse.urlencode({'client_id': 'vikunja-test', 'redirect_uri': redirect, 'response_type': 'code', 'scope': 'openid email profile', 'state': nonce, 'nonce': nonce})
        code = c.follow(path)
        tokenpath = '/oauth2/token'
        userpath = '/oauth2/userinfo'
    else:
        path = '/api/login?' + urllib.parse.urlencode({'clientId': 'vikunja-test', 'redirectUri': redirect, 'responseType': 'code', 'scope': 'openid email profile', 'state': nonce, 'nonce': nonce})
        status, h, b = c.request(path, json.dumps({'application': 'app-anas-vikunja', 'organization': 'anas', 'username': 'llngnormal', 'password': S['old'], 'autoSignin': False, 'type': 'code', 'signinMethod': 'Password'}), 'application/json')
        data = json.loads(b)
        if status != 200 or data.get('status') != 'ok' or (not data.get('data')):
            raise RuntimeError('Casdoor authorization failed')
        code = data['data']
        tokenpath = '/api/login/oauth/access_token'
        userpath = '/api/userinfo'
    if not code:
        raise RuntimeError('empty authorization code')
    status, h, b = c.request(tokenpath, urllib.parse.urlencode({'grant_type': 'authorization_code', 'client_id': 'vikunja-test', 'client_secret': S['oidc'], 'redirect_uri': redirect, 'code': code}))
    data = json.loads(b)
    if status != 200 or not data.get('id_token') or (not data.get('access_token')):
        raise RuntimeError(provider + ' token exchange failed')
    conn = http.client.HTTPConnection('10.253.26.2', 80 if provider == 'llng' else 8000, timeout=30)
    conn.request('GET', userpath, headers={'Host': 'auth.llngtest.invalid', 'Authorization': 'Bearer ' + data['access_token']})
    r = conn.getresponse()
    info = json.loads(r.read())
    conn.close()
    if r.status != 200 or not info.get('sub'):
        raise RuntimeError(provider + ' UserInfo failed')
    return ((time.monotonic() - started) * 1000, info['sub'])

def cgroups():
    names = ['anas_llng_test_1023_' + provider, 'anas_llng_test_1023_postgres', 'anas_llng_test_1023_samba', 'anas_llng_test_1023_anchor']
    if provider == 'llng':
        names.insert(1, 'anas_llng_test_1023_llng_dirwatch')
    if provider == 'casdoor':
        names.insert(1, 'anas_llng_test_1023_dirwatch')
    out = {}
    for name in names:
        pid = subprocess.check_output(D + ['inspect', '--format', '{{.State.Pid}}', name], text=True).strip()
        cg = pathlib.Path('/proc/' + pid + '/cgroup').read_text().strip().split('::', 1)[1]
        out[name.removeprefix('anas_llng_test_1023_')] = pathlib.Path('/sys/fs/cgroup' + cg)
    return out
CG = cgroups()
BASELINE = {}
for name in CG:
    info = json.loads(subprocess.check_output(D + ['inspect', 'anas_llng_test_1023_' + name]))[0]
    BASELINE[name] = {'image': info['Config']['Image'], 'image_id': info['Image'], 'memory_limit': info['HostConfig']['Memory']}
HOST_START = {'loadavg': pathlib.Path('/proc/loadavg').read_text().strip(), 'pressure': pathlib.Path('/proc/pressure/memory').read_text().strip()}

def sample():
    out = {}
    for name, p in CG.items():
        mem = int((p / 'memory.current').read_text())
        m = dict((line.split() for line in (p / 'memory.stat').read_text().splitlines()))
        cpu = dict((line.split() for line in (p / 'cpu.stat').read_text().splitlines()))
        out[name] = {'working_set_bytes': max(0, mem - int(m['inactive_file'])), 'memory_current_bytes': mem, 'swap_bytes': int((p / 'memory.swap.current').read_text()), 'memory_limit': (p / 'memory.max').read_text().strip(), 'memory_events': (p / 'memory.events').read_text().strip(), 'cpu_usec': int(cpu['usage_usec']), 'pids': int((p / 'pids.current').read_text())}
    return {'at': time.monotonic(), 'components': out}

def aggregates(samples):
    out = {}
    for name in CG:
        values = [s['components'][name] for s in samples]
        duration = samples[-1]['at'] - samples[0]['at']
        out[name] = {'working_set_mib_median': round(statistics.median((v['working_set_bytes'] for v in values)) / 2 ** 20, 2), 'working_set_mib_max': round(max((v['working_set_bytes'] for v in values)) / 2 ** 20, 2), 'swap_mib_median': round(statistics.median((v['swap_bytes'] for v in values)) / 2 ** 20, 2), 'swap_mib_max': round(max((v['swap_bytes'] for v in values)) / 2 ** 20, 2), 'memory_limit': values[-1]['memory_limit'], 'memory_events': values[-1]['memory_events'], 'memory_current_mib_median': round(statistics.median((v['memory_current_bytes'] for v in values)) / 2 ** 20, 2), 'cpu_core_percent': round((values[-1]['cpu_usec'] - values[0]['cpu_usec']) / 1000000.0 / duration * 100, 3), 'cpu_seconds': round((values[-1]['cpu_usec'] - values[0]['cpu_usec']) / 1000000.0, 4), 'pids_max': max((v['pids'] for v in values))}
    return out
for n in range(4):
    authenticate(-n - 1)
print(provider + ' warmup passed', flush=True)
idle = []
for n in range(31):
    idle.append(sample())
    time.sleep(1)
print(provider + ' idle sampled', flush=True)
load = []
stop = threading.Event()

def sampler():
    while not stop.is_set():
        load.append(sample())
        stop.wait(0.25)
t = threading.Thread(target=sampler)
t.start()
started = time.monotonic()
try:
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(authenticate, range(60)))
finally:
    stop.set()
    t.join()
    load.append(sample())
duration = time.monotonic() - started
latencies = sorted((r[0] for r in results))
assert len(set((r[1] for r in results))) == 1
report = {'provider': provider, 'baseline': BASELINE, 'host_start': HOST_START, 'method': {'workers': 2 if provider == 'llng' else None, 'users': 1, 'warmups': 4, 'logins': 60, 'concurrency': 2, 'idle_seconds': round(idle[-1]['at'] - idle[0]['at'], 2), 'load_seconds': round(duration, 2), 'login_flow': 'fresh authorization, code exchange and UserInfo; direct HTTP inside isolated namespace; no browser/TLS/network latency', 'memory': 'cgroup v2 memory.current minus inactive_file; CPU percent of one core'}, 'idle': aggregates(idle), 'load': aggregates(load), 'latency_ms': {'median': round(statistics.median(latencies), 2), 'p95': round(latencies[math.ceil(len(latencies) * 0.95) - 1], 2), 'max': round(max(latencies), 2)}, 'successes': len(results), 'same_subject': True, 'host_loadavg': pathlib.Path('/proc/loadavg').read_text().strip(), 'host_memory_pressure': pathlib.Path('/proc/pressure/memory').read_text().strip()}
(P / 'evidence').mkdir(exist_ok=True)
out = P / 'evidence' / ('resources-' + provider + '.json')
out.write_text(json.dumps(report, indent=2))
os.chmod(out, 420)
print(json.dumps(report), flush=True)
