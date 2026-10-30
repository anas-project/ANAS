# TEST_CASES: MAILMOD-T-079
"""Run a disposable VPS relay in a private directory with an SSH stdin lease."""
import json, os, pathlib, re, select, signal, socket, ssl, subprocess, sys, time, urllib.request, shutil

if len(sys.argv) != 2 or not re.fullmatch(r'/tmp/anas-mail-vps-live-[A-Za-z0-9]+', sys.argv[1]):
    raise SystemExit('private fixture directory required')
root = pathlib.Path(sys.argv[1])
st = root.lstat()
if not root.is_dir() or root.is_symlink() or st.st_uid != os.getuid() or st.st_mode & 0o077:
    raise SystemExit('private fixture directory required')
config = json.loads(sys.stdin.readline(65537))
if set(config) != {'source', 'generation', 'notify', 'reader', 'control', 'origin', 'cert', 'key'}:
    raise SystemExit('invalid fixture configuration')
proc = None

def stop(_sig, _frame):
    raise SystemExit('fixture interrupted')
for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
    signal.signal(sig, stop)

def private(path, content):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as f:
        f.write(content)

def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]

try:
    https_port, smtp_port = port(), port()
    if https_port == smtp_port:
        raise RuntimeError('fixture ports collide')
    private(root/'cert.pem', config['cert'])
    private(root/'relay.json', json.dumps({'schema':'anas.mail-vps-runtime/v1', 'directory':str(root/'source'),
        'source_id':config['source'], 'generation':config['generation'], 'domains':['example.test'],
        'capacity_bytes':8388608, 'delivered_days':7, 'listen':'127.0.0.1:'+str(https_port),
        'certificate_host':'127.0.0.1', 'reader':config['reader'], 'control':config['control'],
        'tls_certificate':config['cert'], 'tls_private_key':config['key'],
        'notification_origin':config['origin'], 'notification_secret':config['notify'], 'notification_ca':'',
        'smtp_listen':'127.0.0.1:'+str(smtp_port), 'smtp_hostname':'relay.example.test'}))
    subprocess.run([str(root/'relay'), 'init', str(root/'relay.json')], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
    log = open(root/'relay.log', 'ab', buffering=0)
    proc = subprocess.Popen([str(root/'relay'), str(root/'relay.json')], stdin=subprocess.DEVNULL, stdout=log, stderr=log)
    tls = ssl.create_default_context(cafile=str(root/'cert.pem'))
    for _ in range(60):
        if proc.poll() is not None:
            raise RuntimeError('relay stopped')
        try:
            request = urllib.request.Request('https://127.0.0.1:'+str(https_port)+'/v1/status', headers={'Authorization':'Bearer '+config['reader']})
            with urllib.request.urlopen(request, context=tls, timeout=1) as response:
                if response.status == 200: break
        except Exception:
            time.sleep(.5)
    else:
        raise RuntimeError('relay startup timeout')
    print(json.dumps({'state':'ready','https_port':https_port,'smtp_port':smtp_port}), flush=True)
    readable, _, _ = select.select([sys.stdin], [], [], 600)
    if readable: sys.stdin.readline(128)
finally:
    if proc is not None:
        proc.terminate()
        try: proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            proc.kill(); proc.wait(timeout=5)
    # The whole root is an exclusively created, user-owned test directory.
    if root.is_symlink() or root.lstat().st_uid != os.getuid():
        raise RuntimeError('fixture identity unknown; no automatic removal')
    for name in ('source',):
        path = root/name
        if path.exists():
            if path.is_symlink() or not path.is_dir(): raise RuntimeError('unsafe source path')
            shutil.rmtree(path)
    for path in root.iterdir():
        if path.is_file() and not path.is_symlink(): path.unlink()
        else: raise RuntimeError('unexpected test file; no automatic removal')
    root.rmdir()
    print(json.dumps({'state':'cleaned'}), flush=True)
