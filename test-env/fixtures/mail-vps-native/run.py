# TEST_CASES: MAILMOD-T-017 MAILMOD-T-080 MAILMOD-T-082
"""Run an explicit isolated finance DMS/PG/Roundcube fixture with a VPS source."""
import hashlib, json, os, shlex, subprocess, sys, tarfile
from pathlib import Path

if len(sys.argv) != 3:
    raise SystemExit('private input directory and archive SHA-256 required')
work = Path(sys.argv[1])
if not work.is_absolute() or not work.name.startswith('anas-vps-native-') or work.is_symlink() or work.stat().st_uid != os.getuid() or work.stat().st_mode & 0o077:
    raise SystemExit('private fixture directory required')
archive = work/'bundle.tar'
if hashlib.sha256(archive.read_bytes()).hexdigest() != sys.argv[2]:
    raise SystemExit('fixture archive checksum mismatch')
source = work/'source'; source.mkdir(mode=0o700)
with tarfile.open(archive) as tf:
    if not all(m.isfile() and not Path(m.name).is_absolute() and '..' not in Path(m.name).parts for m in tf.getmembers()):
        raise SystemExit('unsafe archive')
    tf.extractall(source, filter='data')
for path in (source/'test-artifacts').iterdir(): path.chmod(0o700)
outbound = os.environ.get('MAIL_NATIVE_DMS_OUTBOUND') == '1'
milter_probe = os.environ.get('MAIL_DMS_MILTER_PROBE') == '1'
guard = os.environ.get('MAIL_NATIVE_VPS_GUARD_TEST') == '1'
public = os.environ.get('MAIL_NATIVE_VPS_PUBLIC') == '1'
if outbound and (os.environ.get('MAIL_NATIVE_WORKER_TEST') == '1' or (source/'test-env/scripts/server-mail-roundcube-runtime-e2e.py').exists()):
    raise SystemExit('outbound-only fixture requires an isolated DMS-only bundle')
base = Path('/home/whl/anas-mail-go-test-20261024')
root = Path('/home/whl/anas-mail-runtime-20261026')
envs = {}
for line in (base/'daemon.env').read_text().splitlines():
    if line.startswith('export '):
        key, value = line[7:].split('=', 1); envs[key] = shlex.split(value)[0]
if envs['ANAS_TEST_DOCKER_SOCKET'] != '/run/anas-mail-test-1024.sock' or envs['ANAS_TEST_DOCKER_ROOT'] != str(base/'docker') or envs['ANAS_TEST_NETNS'] != 'anas-mail-test-1024' or Path(envs['ANAS_TEST_DOCKER_SOCKET']).exists():
    raise SystemExit('dedicated daemon state not suitable')
daemon=['sudo','-n','env']+[k+'='+v for k,v in envs.items()]+['sh',str(base/'server-isolated-docker.sh')]
def run(args, timeout=700): return subprocess.run(args,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=timeout)
def business():
    p=run(['sudo','-n','docker','ps','-a','--format','{{.ID}} {{.Names}}'])
    if p.returncode: raise RuntimeError('business inventory unavailable')
    return sorted(p.stdout.decode().splitlines())
def save(name, data):
    path=work/name;path.write_bytes(data);path.chmod(0o600)
before=business(); failure=None; cleanup=[]; started=False
parent=root/('anas-mail-runtime-test-'+work.name)
try:
    if parent.exists(): raise RuntimeError('test parent exists')
    p=run(daemon+['start']);started=True;save('daemon-start.log',p.stdout)
    if p.returncode: raise RuntimeError('isolated daemon start failed')
    parent.mkdir(mode=0o700)
    worker = os.environ.get('MAIL_NATIVE_WORKER_TEST') == '1'
    crosshost = os.environ.get('MAIL_NATIVE_VPS_CROSSHOST') == '1'
    resend_live = os.environ.get('MAIL_NATIVE_RESEND_PUBLIC') == '1'
    if milter_probe and (worker or crosshost or public or guard or outbound or resend_live or os.environ.get('MAIL_DMS_FILTER_PROBE') == '1' or os.environ.get('MAIL_DMS_INTAKE_NORMAL_ONLY') == '1' or os.environ.get('MAIL_NATIVE_TEST_HLONG_DOMAIN') == '1' or (source/'test-env/scripts/server-mail-roundcube-runtime-e2e.py').exists()): raise RuntimeError('Milter observation requires an isolated DMS-only bundle')
    if guard and (worker or crosshost or public or outbound or resend_live or os.environ.get('MAIL_DMS_FILTER_PROBE') == '1' or os.environ.get('MAIL_DMS_INTAKE_NORMAL_ONLY') == '1' or os.environ.get('MAIL_NATIVE_TEST_HLONG_DOMAIN') == '1'): raise RuntimeError('guard fixture cannot combine modes')
    if public and (worker or crosshost or outbound or resend_live or os.environ.get('MAIL_DMS_FILTER_PROBE') == '1' or os.environ.get('MAIL_DMS_INTAKE_NORMAL_ONLY') == '1' or os.environ.get('MAIL_NATIVE_TEST_HLONG_DOMAIN') == '1'): raise RuntimeError('public fixture cannot combine modes')
    if resend_live and (worker or crosshost or outbound): raise RuntimeError('Resend fixture cannot combine modes')
    if crosshost and (worker or outbound): raise RuntimeError('cross-host fixture cannot combine modes')
    if worker:
        binding, bridge = work/'worker-native.json', work/'bridge'
        if not binding.is_file() or binding.is_symlink() or binding.stat().st_uid != os.getuid() or binding.stat().st_mode & 0o077 or binding.stat().st_size > 16384 or bridge.is_symlink() or not bridge.is_dir() or bridge.stat().st_uid != os.getuid() or bridge.stat().st_mode & 0o077:
            raise RuntimeError('private Worker fixture input required')
    if crosshost:
        binding, bridge = work/'vps-crosshost.json', work/'bridge'
        if not binding.is_file() or binding.is_symlink() or binding.stat().st_uid != os.getuid() or binding.stat().st_mode & 0o077 or binding.stat().st_size > 16384 or bridge.is_symlink() or not bridge.is_dir() or bridge.stat().st_uid != os.getuid() or bridge.stat().st_mode & 0o077:
            raise RuntimeError('private cross-host fixture input required')
    if public:
        binding, bridge = work/'vps-public.json', work/'bridge'
        if not binding.is_file() or binding.is_symlink() or binding.stat().st_uid != os.getuid() or binding.stat().st_mode & 0o077 or binding.stat().st_size > 16384 or bridge.is_symlink() or not bridge.is_dir() or bridge.stat().st_uid != os.getuid() or bridge.stat().st_mode & 0o077:
            raise RuntimeError('private public fixture input required')
    domain_fixture = os.environ.get('MAIL_NATIVE_TEST_HLONG_DOMAIN') == '1'
    if domain_fixture and (worker or crosshost or outbound or resend_live): raise RuntimeError('domain fixture cannot combine modes')
    env=dict(os.environ,MAIL_RUNTIME_TEST_ROOT=str(parent),DOCKER_CMD=str(root/'docker-test'),ANAS_TEST_DOCKER_ROOT=envs['ANAS_TEST_DOCKER_ROOT'],MAIL_NATIVE_VPS_TEST='0' if worker or outbound or resend_live or domain_fixture or guard or public or milter_probe else '1')
    if worker:
        env.update(MAIL_NATIVE_WORKER_INPUT=str(binding),MAIL_NATIVE_WORKER_BRIDGE=str(bridge))
    if crosshost:
        env.update(MAIL_NATIVE_VPS_CROSSHOST_INPUT=str(binding),MAIL_NATIVE_VPS_CROSSHOST_BRIDGE=str(bridge))
    if public:
        env.update(MAIL_NATIVE_VPS_PUBLIC_INPUT=str(binding),MAIL_NATIVE_VPS_PUBLIC_BRIDGE=str(bridge))
    for option in ['MAIL_DMS_COMBINED_ONLY','MAIL_DMS_CROSSHOST_SKIP_INTAKE','MAIL_DMS_WORKER_SKIP_INTAKE','MAIL_DMS_DOMAIN_SKIP_INTAKE','MAIL_DMS_GUARD_SKIP_INTAKE','MAIL_DMS_PUBLIC_SKIP_INTAKE','MAIL_DMS_NATIVE_OUTBOUND','MAIL_DMS_NATIVE_INTAKE_QUOTA','MAIL_DMS_TRANSFER_REJECT_TEST']: env.pop(option,None)
    # Keep the DMS cold-start readback that creates the initial test account;
    # skip only the standalone intake suite, whose Sieve case remains failing.
    if crosshost and os.environ.get('MAIL_NATIVE_CROSSHOST_COMBINED_ONLY') == '1':
        env['MAIL_DMS_CROSSHOST_SKIP_INTAKE']='1'
    if guard:
        env['MAIL_DMS_GUARD_SKIP_INTAKE']='1'
    if public:
        env['MAIL_DMS_PUBLIC_SKIP_INTAKE']='1'
    if worker and os.environ.get('MAIL_NATIVE_WORKER_SKIP_INTAKE') == '1':
        env['MAIL_DMS_WORKER_SKIP_INTAKE']='1'
    if domain_fixture:
        env['MAIL_DMS_DOMAIN_SKIP_INTAKE']='1'
    if outbound: env['MAIL_DMS_NATIVE_OUTBOUND']='1'
    p=subprocess.run(['python3',str(source/'test-env/scripts/server-mail-dms-runtime-e2e.py')],env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=2150 if os.environ.get('MAIL_NATIVE_WORKER_RETRY_TEST') == '1' else 1800 if public else 1000 if crosshost else 700)
    save('test.log',p.stdout)
    if p.returncode: raise RuntimeError('isolated native combined test failed')
except BaseException as exc: failure=type(exc).__name__+': '+str(exc)
finally:
    if started:
        try:
            p=run(daemon+['stop']);save('daemon-stop.log',p.stdout)
            if p.returncode: cleanup.append('isolated daemon stop failed')
        except BaseException: cleanup.append('isolated daemon stop unknown')
    try: unchanged=business()==before
    except BaseException: unchanged=False
    if not unchanged: cleanup.append('business inventory changed or unknown')
    result={'failure':failure,'cleanup_errors':cleanup,'business_unchanged':unchanged,'parent':str(parent),
            'native_intake_executed':True if os.environ.get('MAIL_DMS_INTAKE_NORMAL_ONLY') == '1' else False if (os.environ.get('MAIL_NATIVE_WORKER_TEST') == '1' and os.environ.get('MAIL_NATIVE_WORKER_SKIP_INTAKE') == '1' or os.environ.get('MAIL_NATIVE_VPS_CROSSHOST') == '1' and os.environ.get('MAIL_NATIVE_CROSSHOST_COMBINED_ONLY') == '1' or os.environ.get('MAIL_NATIVE_TEST_HLONG_DOMAIN') == '1' or os.environ.get('MAIL_DMS_FILTER_PROBE') == '1' or guard or public) else None,
            'native_intake_normal_only':os.environ.get('MAIL_DMS_INTAKE_NORMAL_ONLY') == '1',
            'milter_observation_only':milter_probe,
            'bound_copy_experiment':milter_probe and os.environ.get('MAIL_DMS_BOUND_COPY_PROBE')=='1'}
    if milter_probe: result['native_intake_executed'] = False
    save('final.json',json.dumps(result,indent=2).encode());print(json.dumps(result),flush=True)
if failure or cleanup: raise SystemExit(1)
