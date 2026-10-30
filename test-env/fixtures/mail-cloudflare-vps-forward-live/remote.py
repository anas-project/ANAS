# TEST_CASES: MAILMOD-T-009 MAILMOD-T-080
"""Disposable IPv6-only SMTP relay for a controlled Cloudflare forward test.

The caller controls its SSH stdin lease. It does not create DNS, provider
rules or credentials; shutdown removes only this private test directory.
"""
import hashlib, json, os, pathlib, re, select, shutil, signal, socket, ssl, subprocess, sys, time, urllib.request

if len(sys.argv) != 3 or not re.fullmatch(r'/tmp/anas-mail-cf-vps-[a-z0-9]{8,32}', sys.argv[1]) or not re.fullmatch(r'[a-f0-9]{64}', sys.argv[2]):
    raise SystemExit('explicit private test directory and binary checksum required')
root = pathlib.Path(sys.argv[1])
st = root.lstat()
if root.is_symlink() or not root.is_dir() or st.st_uid != os.getuid() or st.st_mode & 0o077:
    raise SystemExit('private test directory required')
binary = root/'relay'
if binary.is_symlink() or not binary.is_file() or binary.stat().st_uid != os.getuid() or hashlib.sha256(binary.read_bytes()).hexdigest() != sys.argv[2]:
    raise SystemExit('test binary checksum mismatch')
config = json.loads(sys.stdin.readline(65537))
required = {'source','generation','reader','control','cert','key'}
if set(config) not in (required, required | {'lease_seconds'}, required | {'notify','origin'}, required | {'lease_seconds','notify','origin'}):
    raise SystemExit('invalid test configuration')
if 'notify' in config and (not re.fullmatch(r'[a-f0-9]{64}', config['notify']) or config['notify'] in (config['reader'], config['control']) or not re.fullmatch(r'https://[a-z0-9-]+\.trycloudflare\.com', config['origin'])):
    raise SystemExit('invalid notification configuration')
lease_seconds = config.get('lease_seconds', 900)
if type(lease_seconds) is not int or lease_seconds not in (900, 1500):
    raise SystemExit('invalid bounded fixture lease')
proc = None
capability = False
stage = 'configuration'

def stop(_sig, _frame):
    raise SystemExit('test interrupted')
for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP): signal.signal(sig, stop)

def private(path, content):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as f: f.write(content)

def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1',0))
        return sock.getsockname()[1]

try:
    https_port = port()
    private(root/'cert.pem',config['cert'])
    private(root/'relay.json',json.dumps({'schema':'anas.mail-vps-runtime/v1','directory':str(root/'source'),
        'source_id':config['source'],'generation':config['generation'],'domains':['mail-poc.bengbu.cc.cd'],
        'capacity_bytes':16<<20,'delivered_days':7,'listen':'127.0.0.1:'+str(https_port),'certificate_host':'127.0.0.1',
        'reader':config['reader'],'control':config['control'],'tls_certificate':config['cert'],'tls_private_key':config['key'],
        'notification_origin':config.get('origin',''),'notification_secret':config.get('notify',''),'notification_ca':'','smtp_listen':'[2603:c023:8:a900::a]:25',
        'smtp_hostname':'vps-relay.mail-poc.bengbu.cc.cd','forward_domain':'vps-target.mail-poc.bengbu.cc.cd'}))
    stage = 'initialization'
    subprocess.run([str(binary),'init',str(root/'relay.json')],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=30)
    stage = 'restricted-port-capability'
    subprocess.run(['sudo','-n','/usr/sbin/setcap','cap_net_bind_service=+ep',str(binary)],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=10)
    capability = True
    stage = 'relay-startup'
    log = open(root/'relay.log','ab',buffering=0)
    proc = subprocess.Popen([str(binary),str(root/'relay.json')],stdin=subprocess.DEVNULL,stdout=log,stderr=log)
    tls = ssl.create_default_context(cafile=str(root/'cert.pem'))
    for _ in range(60):
        if proc.poll() is not None: raise RuntimeError('relay stopped before readiness')
        try:
            request = urllib.request.Request('https://127.0.0.1:'+str(https_port)+'/v1/status',headers={'Authorization':'Bearer '+config['reader']})
            with urllib.request.urlopen(request,context=tls,timeout=1) as response:
                if response.status == 200: break
        except Exception: time.sleep(.5)
    else: raise RuntimeError('relay readiness timeout')
    print(json.dumps({'state':'ready','https_port':https_port}),flush=True)
    # Bound the public listener and secrets even if the controlling SSH client disappears.
    readable, _, _ = select.select([sys.stdin],[],[],lease_seconds)
    if readable: sys.stdin.readline(128)
except BaseException as exc:
    print('isolated fixture phase='+stage+' failure='+type(exc).__name__,file=sys.stderr,flush=True)
    raise
finally:
    if proc is not None:
        proc.terminate()
        try: proc.wait(timeout=15)
        except subprocess.TimeoutExpired: proc.kill(); proc.wait(timeout=5)
    if capability:
        subprocess.run(['sudo','-n','/usr/sbin/setcap','-r',str(binary)],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=10)
    if root.is_symlink() or root.lstat().st_uid != os.getuid(): raise RuntimeError('fixture identity unknown; no removal')
    for path in root.iterdir():
        if path.is_symlink(): raise RuntimeError('unexpected symlink; no removal')
        if path.is_dir(): shutil.rmtree(path)
        elif path.is_file(): path.unlink()
        else: raise RuntimeError('unexpected file; no removal')
    root.rmdir()
    print(json.dumps({'state':'cleaned'}),flush=True)
