# TEST_CASES: MAILMOD-T-078, MAILMOD-T-079
"""Disposable local-only PostgreSQL and TLS server for explicit live tests."""
import json, os, pathlib, secrets, select, signal, socket, ssl, subprocess, sys, time, urllib.request

if len(sys.argv) != 2:
    raise SystemExit('private fixture directory required')
root = pathlib.Path(sys.argv[1])
if not root.is_absolute() or not root.is_dir() or root.stat().st_mode & 0o077:
    raise SystemExit('private fixture directory required')
config = json.loads(sys.stdin.readline(65537))
if set(config) != {'source', 'generation', 'secret', 'test_token', 'cert', 'key'}:
    raise SystemExit('invalid fixture configuration')
image = 'ghcr.io/anas-project/anas-postgres:18.4.0-r5'
expected = 'sha256:339c4dd98a38d35ef9ad4c9164231c9905c4cbd046287813fb8170f5f3ddd362'
name = 'anas_mail_live_notice_' + secrets.token_hex(6)
label = secrets.token_hex(16)
container = None
server = None

def stop(_signum, _frame):
    raise SystemExit('fixture interrupted')
for signum in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
    signal.signal(signum, stop)

def call(args, data=None, check=True, timeout=45):
    p = subprocess.run(args, input=data, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
    if check and p.returncode:
        raise RuntimeError('fixture command failed: ' + args[1])
    return p

def docker(*args, **kw):
    return call(['docker', *args], **kw)

def private(path, contents):
    path.write_text(contents)
    path.chmod(0o600)

try:
    assert docker('image', 'inspect', image, '--format', '{{.Id}}').stdout.decode().strip() == expected
    password, role_password = secrets.token_hex(32), secrets.token_hex(32)
    private(root/'pg.env', 'POSTGRES_USER=fixture_bootstrap\nPOSTGRES_DB=postgres\nPOSTGRES_PASSWORD='+password+'\n')
    private(root/'cert.pem', config['cert']); private(root/'key.pem', config['key'])
    container = docker('create', '--pull', 'never', '--name', name, '--label', 'anas.mail.live-notice='+label,
                       '--network', 'bridge', '-p', '127.0.0.1::5432', '--memory', '256m', '--pids-limit', '128', '--env-file', str(root/'pg.env'), image).stdout.decode().strip()
    assert len(container) == 64
    docker('start', container)
    for _ in range(90):
        if docker('exec', container, 'pg_isready', '-h', '127.0.0.1', '-U', 'fixture_bootstrap', '-d', 'postgres', check=False).returncode == 0:
            break
        if docker('inspect', '--format', '{{.State.Running}}', container, check=False).stdout.decode().strip() != 'true':
            raise RuntimeError('fixture PostgreSQL stopped')
        time.sleep(1)
    else:
        raise RuntimeError('fixture PostgreSQL timeout')
    sql = "CREATE ROLE anas_mail_model LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '"+role_password+"';\nCREATE DATABASE anas_mail_model_1025 OWNER anas_mail_model;\n"
    docker('exec', '-i', container, 'sh', '-c', 'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -U fixture_bootstrap -d postgres -v ON_ERROR_STOP=1', data=sql.encode())
    ports = json.loads(docker('inspect', '--format', '{{json .NetworkSettings.Ports}}', container).stdout)
    binding = ports['5432/tcp']; assert len(binding) == 1 and binding[0]['HostIp'] == '127.0.0.1'
    dbport = int(binding[0]['HostPort'])
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0)); port = s.getsockname()[1]
    private(root/'server.json', json.dumps({'dsn':'postgres://anas_mail_model:'+role_password+'@127.0.0.1:'+str(dbport)+'/anas_mail_model_1025?sslmode=disable',
          'source':config['source'],'generation':config['generation'],'secret':config['secret'],'test_token':config['test_token'],
          'cert':str(root/'cert.pem'),'key':str(root/'key.pem'),'port':port}))
    logfile = open(root/'server.log', 'ab', buffering=0)
    server = subprocess.Popen([str(root/'receiver'), str(root/'server.json')], stdin=subprocess.DEVNULL, stdout=logfile, stderr=logfile)
    tls = ssl.create_default_context(cafile=str(root/'cert.pem'))
    for _ in range(60):
        if server.poll() is not None:
            raise RuntimeError('fixture receiver stopped')
        try:
            with urllib.request.urlopen('https://127.0.0.1:'+str(port)+'/__test/health', context=tls, timeout=1) as r:
                if r.status == 204: break
        except Exception:
            time.sleep(.5)
    else:
        raise RuntimeError('fixture receiver timeout')
    print(json.dumps({'state':'ready','port':port}), flush=True)
    # The open SSH stdin is the lease. EOF or a bounded timeout cleans all state.
    readable, _, _ = select.select([sys.stdin], [], [], 600)
    if readable: sys.stdin.readline(128)
finally:
    if server is not None:
        server.terminate()
        try: server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill(); server.wait(timeout=5)
    if container:
        observed = docker('inspect', '--format', '{{index .Config.Labels "anas.mail.live-notice"}}', container, check=False)
        if observed.returncode or observed.stdout.decode().strip() != label:
            raise RuntimeError('fixture identity unknown; no automatic removal')
        docker('rm', '-f', '-v', container)
        if docker('inspect', container, check=False).returncode == 0:
            raise RuntimeError('fixture container cleanup unconfirmed')
    for filename in ('pg.env','cert.pem','key.pem','server.json','server.log'):
        try: (root/filename).unlink()
        except FileNotFoundError: pass
    print(json.dumps({'state':'cleaned'}), flush=True)
