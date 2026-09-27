#!/usr/bin/env python3
"""Explicit disposable-VM Incus lifecycle test. Never use on a business host.

The exact cloud-init identity must be passed by its owner. Uses a fresh daemon
in that VM, not the physical host daemon. No Docker command is executed.
The container rootfs is a tiny measured test fixture, not a distrobuilder
product image. The VM tier needs nested KVM in the lab VM; its fixture is an
upstream VM image with the same fixture program pushed in and published once,
also not a product image.
"""
import argparse
import base64
import hashlib
import io
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tarfile
import time

ROOT = Path('/run/anas-incus-lifecycle')
# Debian 13 mounts /run noexec; the pinned Provider copy must live elsewhere.
PROVIDER_ROOT = Path('/var/lib/anas-incus-lifecycle')
# VM fixture bytes are too large for the tmpfs runtime root.
IMAGE_ROOT = Path('/var/tmp/anas-incus-lifecycle-image')
PROJECTS = ('anas-lifecycle-a', 'anas-lifecycle-b')
POOL = 'anas-lifecycle-btrfs'
TIERS = {
    'container': {'interface': 'incus_container', 'test': 'TestNativeIncusContainerLeaseLifecycle',
                  'pool_gib': 12, 'go_timeout': '9m', 'timeout': 570,
                  'overrides': ('unix-char', 'unix-block', 'privileged', 'raw-lxc')},
    'vm': {'interface': 'incus_vm', 'test': 'TestNativeIncusVMLeaseLifecycle',
           'pool_gib': 22, 'go_timeout': '17m', 'timeout': 1080,
           'overrides': ('pci', 'raw-qemu'), 'extra': ('nested-virtualization-fence',)},
}
VM_BUILD = 'anas-vmfixture-build'
VM_BASE_ALIAS = 'anas-vmfixture-base'
LAB_INSTANCES = ('anas-native-job', 'anas-native-overquota', 'anas-native-timed')


def required_lifecycle_tests(tier='container'):
    parent = TIERS[tier]['test']
    overrides = ('cpu', 'memory', 'disk', 'host-disk', 'other-project-network', 'proxy', 'gpu', 'usb') + TIERS[tier]['overrides']
    return {parent, *(parent+'/'+name for name in (
        'same-name-project-isolation', 'daemon-instance-quota',
        'daemon-rejects-direct-quota-and-device-overrides', 'stdin-secret',
        'management-certificate-rotation', 'root-disk-quota',
        'exec-cancel-and-independent-reclaim', 'stop-delete-idempotent', 'typical-job-wall-time') + TIERS[tier].get('extra', ())),
        *(parent+'/daemon-rejects-direct-quota-and-device-overrides/'+name for name in overrides)}


def lifecycle_results_passed(events, returncode, tier='container'):
    passed = {event.get('Test') for event in events if event.get('Action') == 'pass'}
    return (returncode == 0
            and not any(event.get('Action') in ('fail', 'skip') for event in events)
            and required_lifecycle_tests(tier).issubset(passed)
            and any(event.get('Action') == 'pass' and not event.get('Test') for event in events))


def lifecycle_metrics(events):
    metrics = []
    for event in events:
        match = re.search(r'(lease_[01]_create_start_ready_ms=\d+|actual_guest_write_bytes=\d+ limit_bytes=\d+'
                          r'|typical_job tier=incus_[a-z]+ ready_ms=\d+ exec_ms=\d+ reclaim_ms=\d+ wall_ms=\d+'
                          r'|nested_virtualization daemon_restriction=(?:true|false) guest_virtualization_flags=\d+)', event.get('Output', ''))
        if match:
            metrics.append(match.group(1))
    return metrics


def require_vm(identity):
    if (os.geteuid() != 0 or not re.fullmatch(r'anas-incus-lifecycle-[a-z0-9]{6}', identity)
            or Path('/var/lib/cloud/data/instance-id').read_text().strip() != identity
            or Path('/sys/class/dmi/id/sys_vendor').read_text().strip() != 'QEMU'
            or Path('/var/run/docker.sock').exists() or Path('/var/lib/docker').exists()):
        raise RuntimeError('exact disposable QEMU cloud identity without Docker is required')


def call(args, env=None, timeout=90, data=None, check=True):
    result = subprocess.run(args, input=data, stdin=None if data is not None else subprocess.DEVNULL,
                            env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if len(result.stdout) > 4 << 20 or len(result.stderr) > 64 << 10:
        raise RuntimeError('test subprocess output exceeded its limit')
    if check and result.returncode:
        # Error details remain private; never print command arguments or keys.
        raise RuntimeError('test subprocess failed: ' + Path(args[0]).name)
    return result


def emit(name, **fields):
    print(json.dumps({'check': name, 'passed': True, **fields}), flush=True)


def create_fixture_image(binary, target):
    with tarfile.open(target, 'w:xz') as archive:
        for name in ('dev', 'proc', 'sys', 'run', 'etc', 'sbin', 'usr', 'usr/bin', 'usr/local', 'usr/local/bin'):
            item = tarfile.TarInfo('rootfs/' + name); item.type = tarfile.DIRTYPE; item.mode = 0o755; archive.addfile(item)
        payload = binary.read_bytes()
        item = tarfile.TarInfo('rootfs/usr/local/bin/anas-fixture'); item.mode = 0o755; item.size = len(payload)
        archive.addfile(item, io.BytesIO(payload))
        for name in ('sbin/init', 'usr/bin/test'):
            item = tarfile.TarInfo('rootfs/' + name); item.type = tarfile.SYMTYPE
            item.linkname = '/usr/local/bin/anas-fixture'; item.mode = 0o777; archive.addfile(item)
        body = b'architecture: x86_64\ncreation_date: 1789948800\nproperties:\n  description: ANAS isolated lifecycle fixture, not a product image\n  os: ANAS-test\n'
        item = tarfile.TarInfo('metadata.yaml'); item.mode = 0o644; item.size = len(body); archive.addfile(item, io.BytesIO(body))
    return hashlib.sha256(target.read_bytes()).hexdigest()


def build_vm_fixture_image(cli, base_metadata, base_disk, binary):
    """Publish one measured VM fixture from an upstream VM image.

    The build guest lives in the default project, needs no network, and only
    receives the fixture program through the agent. Its published bytes are
    exported so every lease imports the same measured file, exactly like the
    container fixture.
    """
    for source in (base_metadata, base_disk):
        if not source.is_absolute() or not source.is_file() or source.is_symlink():
            raise RuntimeError('explicit regular upstream VM image inputs required')
    IMAGE_ROOT.mkdir(mode=0o700)
    cli('image', 'import', str(base_metadata), str(base_disk), '--alias', VM_BASE_ALIAS, timeout=600)
    # The published image keeps the build VM's root size; an unset root would
    # take the 10GiB VM default and never fit a 4GiB lease quota.
    cli('init', VM_BASE_ALIAS, VM_BUILD, '--vm', '--storage', POOL, '--device', 'root,size=4GiB', timeout=600)
    cli('start', VM_BUILD, timeout=180)
    deadline = time.monotonic() + 300
    while cli('exec', VM_BUILD, '--', 'true', check=False, timeout=30).returncode:
        if time.monotonic() > deadline:
            raise RuntimeError('upstream VM image agent never became ready')
        time.sleep(3)
    cli('file', 'push', str(binary), VM_BUILD + '/usr/local/bin/anas-fixture', '--mode=0755', '--uid=0', '--gid=0', timeout=120)
    # Flush the pushed program, then stop. A nested guest occasionally ignores
    # the ACPI request; after a bounded graceful attempt force it off, which
    # is safe once the file is synced (the fixture is measured after publish).
    cli('exec', VM_BUILD, '--', 'sync', timeout=60)
    if cli('stop', VM_BUILD, '--timeout=90', check=False, timeout=120).returncode:
        cli('stop', VM_BUILD, '--force', timeout=120)
    cli('publish', VM_BUILD, '--alias', 'anas-vmfixture', '--compression=none', timeout=1200)
    # CLI list filters are prefix matches ("anas-vmfixture" also matches the
    # base alias); select by the exact alias instead.
    published = [item for item in json.loads(cli('image', 'list', '--format=json').stdout)
                 if 'anas-vmfixture' in {alias.get('name') for alias in item.get('aliases') or []}]
    if len(published) != 1:
        raise RuntimeError('published VM fixture is ambiguous')
    cli('image', 'export', published[0]['fingerprint'], str(IMAGE_ROOT/'vm-fixture'), timeout=1200)
    exported = sorted(IMAGE_ROOT.iterdir())
    if len(exported) != 1 or not exported[0].name.startswith('vm-fixture'):
        raise RuntimeError('VM fixture must export as one unified image file')
    digest = hashlib.sha256()
    with exported[0].open('rb') as stream:
        for block in iter(lambda: stream.read(1 << 20), b''):
            digest.update(block)
    if digest.hexdigest() != published[0]['fingerprint']:
        raise RuntimeError('exported VM fixture bytes do not match the published fingerprint')
    cli('delete', VM_BUILD, timeout=180)
    cli('image', 'delete', 'anas-vmfixture', timeout=120)
    cli('image', 'delete', VM_BASE_ALIAS, timeout=120)
    return exported[0], digest.hexdigest()


def prepare_rotation_fixture(provider, environments):
    """Pin the actual Provider used during the running-guest rotation check.

    This remains private VM-only test input, never a consumer mount or portable
    report: the environment includes the old management credential.
    """
    if len(environments) != 2 or any(
            item.get('INCUS_ENDPOINT') != 'https://127.0.0.1:8443'
            or item.get('ANAS_RESOURCE_SANDBOX') != project
            for item, project in zip(environments, PROJECTS)):
        raise RuntimeError('rotation fixture must use the two local lab leases')
    source = Path(provider)
    before = source.lstat()
    if not stat.S_ISREG(before.st_mode) or not 0 < before.st_size <= 64 << 20:
        raise RuntimeError('bounded precompiled Provider fixture required')
    with source.open('rb') as stream:
        body = stream.read((64 << 20) + 1)
        after = os.fstat(stream.fileno())
    identity = lambda value: (value.st_dev, value.st_ino, value.st_size, value.st_mtime_ns, value.st_ctime_ns)
    if len(body) != before.st_size or identity(before) != identity(after) or identity(before) != identity(source.lstat()):
        raise RuntimeError('Provider input changed during rotation fixture preparation')
    record = json.dumps({'schema': 'anas.incus-native-management-rotation/v1',
                         'provider_sha256': hashlib.sha256(body).hexdigest(),
                         'environments': environments}, sort_keys=True).encode() + b'\n'
    if len(record) > 128 << 10:
        raise RuntimeError('rotation fixture exceeds its size limit')
    PROVIDER_ROOT.mkdir(mode=0o700)
    with (PROVIDER_ROOT/'provider').open('xb') as output:
        output.write(body)
    os.chmod(PROVIDER_ROOT/'provider', 0o700)
    descriptor = os.open(ROOT/'rotation.json', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'wb') as output:
        output.write(record)


def main(args):
    require_vm(args.vm_id)
    tier = TIERS[args.interface]
    if args.interface == 'vm' and not Path('/dev/kvm').exists():
        raise RuntimeError('the VM tier needs nested KVM inside the disposable lab VM')
    for name in ('provider', 'tests', 'guest_binary', 'test2json'):
        p = Path(getattr(args, name))
        if not p.is_absolute() or not p.is_file() or p.is_symlink():
            raise RuntimeError('explicit regular precompiled test inputs required')
    report = Path(args.report_root)
    if not report.is_absolute() or report.exists() or ROOT.exists() or PROVIDER_ROOT.exists():
        raise RuntimeError('fresh report and runtime directories required')
    os.umask(0o077)
    report.mkdir(mode=0o700)
    ROOT.mkdir(mode=0o700)
    (ROOT/'identity').write_text(args.vm_id+'\n')
    (ROOT/'admin').mkdir(mode=0o700)
    env = {'PATH':'/usr/sbin:/usr/bin:/sbin:/bin','HOME':str(ROOT/'admin'),
           'INCUS_CONF':str(ROOT/'admin'),'INCUS_DIR':'/var/lib/incus','INCUS_SOCKET':'/var/lib/incus/unix.socket'}

    def cli(*arguments, **options):
        return call(['/usr/bin/incus', '--force-local', *arguments], env, **options)

    call(['/usr/bin/systemctl','start','incus.service'])
    cli('admin','waitready',timeout=60)
    for kind in ('storage', 'image', 'list'):
        arguments = ('list','--format=json') if kind != 'list' else ('--format=json',)
        if json.loads(cli(kind,*arguments).stdout):
            raise RuntimeError('lab daemon must have no existing pools, images or instances')
    projects=json.loads(cli('project','list','--format=json').stdout)
    if [p['name'] for p in projects] != ['default']:
        raise RuntimeError('lab daemon must have only its default project')
    if args.interface == 'vm' and not (args.vm_base_metadata and args.vm_base_disk):
        raise RuntimeError('the VM tier needs explicit upstream VM image inputs')
    pin=None
    try:
        cli('storage','create',POOL,'btrfs','size=%dGiB' % tier['pool_gib'])
        if args.interface == 'vm':
            image,pin=build_vm_fixture_image(cli,Path(args.vm_base_metadata),Path(args.vm_base_disk),Path(args.guest_binary))
        else:
            image=report/'fixture.tar.xz'
            pin=create_fixture_image(Path(args.guest_binary),image)
        emit('measured_fixture_image',fingerprint=pin,product_image=False,interface=tier['interface'])
        pool_path=Path('/var/lib/incus/storage-pools')/POOL
        fs=os.statvfs(pool_path)
        if fs.f_bavail*fs.f_frsize < 8<<30:
            raise RuntimeError('quota positive control requires eight GiB free in the test pool')
        emit('real_btrfs_pool_created')
        for name in ('manager','manager-next','consumer-a','consumer-b'):
            call(['/usr/bin/openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(ROOT/(name+'.key')),
                  '-out',str(ROOT/(name+'.crt')),'-days','1','-subj','/CN=anas-lifecycle-'+name],timeout=20)
        cli('config','trust','add-certificate',str(ROOT/'manager.crt'),'--name=anas-lifecycle-manager')
        cli('config','set','core.https_address','127.0.0.1:8443')
        server=Path('/var/lib/incus/server.crt').read_bytes()
        server_der=base64.b64decode(b''.join(x for x in server.splitlines() if not x.startswith(b'-----')))
        encode=lambda p:base64.b64encode(p.read_bytes()).decode()
        leases=[]
        provider_environments=[]
        for project,name in zip(PROJECTS,('consumer-a','consumer-b')):
            # Preload real measured image bytes before ensure, without using a
            # fake allowlist or inventing a Provider result/supply protocol.
            cli('project','create',project,'-c','features.networks=false','-c','features.images=true','-c','features.profiles=true')
            cli('image','import',str(image),'--project',project,timeout=600)
            provider_env={'PATH':env['PATH'],'INCUS_ENDPOINT':'https://127.0.0.1:8443','INCUS_SERVER_CERT_B64':base64.b64encode(server).decode(),
                          'INCUS_ADMIN_CERT_B64':encode(ROOT/'manager.crt'),'INCUS_ADMIN_KEY_B64':encode(ROOT/'manager.key'),
                          'ANAS_RESOURCE_CLIENT_CERT':encode(ROOT/(name+'.crt')),'INCUS_STORAGE_POOL':POOL,
                          'INCUS_NETWORK_IPV6':'false','ANAS_RESOURCE_CONSUMER':name.replace('-','_'),
                          'ANAS_RESOURCE_SANDBOX':project,'ANAS_RESOURCE_INSTANCE_PREFIX':'anas-native-',
                          'ANAS_RESOURCE_IMAGE_ARCHITECTURE':'amd64','ANAS_RESOURCE_MAX_INSTANCES':'1',
                          'ANAS_RESOURCE_CPU':'1','ANAS_RESOURCE_MEMORY_MIB':'512','ANAS_RESOURCE_DISK_GIB':'4','ANAS_RESOURCE_IMAGE_ALLOWLIST':pin}
            provider_environments.append(provider_env.copy())
            for operation in ('ensure','ensure','inspect'):
                result=call([args.provider,operation,'--isolation',args.interface],provider_env,check=False)
                if result.returncode:
                    (report/(project+'-'+operation+'-failure.txt')).write_bytes(result.stderr)
                    raise RuntimeError('real Provider '+operation+' failed; private diagnostic retained')
                if json.loads(result.stdout) != {'exists':True,'ready':True,'restricted':True,'quota_enforced':True}:
                    raise RuntimeError('Provider did not confirm complete lease')
            emit('provider_ensure_repeat_inspect',project=project)
            leases.append({'Interface':tier['interface'],'Endpoint':'https://127.0.0.1:8443','Sandbox':project,
                           'InstancePrefix':'anas-native-','Profile':'anas-lease','ServerCertFingerprint':hashlib.sha256(server_der).hexdigest(),
                           'ServerCertB64':base64.b64encode(server).decode(),'ClientCertB64':encode(ROOT/(name+'.crt')),
                           'ClientKeyB64':encode(ROOT/(name+'.key')),'ImageAllowlist':[pin],'MaxInstances':1,'CPU':1,'MemoryMiB':512,'DiskGiB':4})
        (ROOT/'leases.json').write_text(json.dumps(leases))
        prepare_rotation_fixture(args.provider, provider_environments)
        package='github.com/anas-project/ANAS/internal/computeclient'
        with (report/'lifecycle.jsonl').open('x') as output:
            result=subprocess.run([args.test2json,'-t','-p',package,args.tests,'-test.v=test2json','-test.count=1',
                                   '-test.timeout='+tier['go_timeout'],'-test.run=^'+tier['test']+'$'],
                                  env={'PATH':env['PATH'],'GOMAXPROCS':'1','ANAS_REQUIRE_INCUS_LIFECYCLE_NATIVE':'1'},
                                  stdin=subprocess.DEVNULL,stdout=output,stderr=subprocess.STDOUT,timeout=tier['timeout'])
        events=[json.loads(line) for line in (report/'lifecycle.jsonl').read_text().splitlines()]
        required=required_lifecycle_tests(args.interface)
        if not lifecycle_results_passed(events, result.returncode, args.interface):
            print(json.dumps({'failed_tests':[e.get('Test') for e in events if e.get('Action')=='fail' and e.get('Test')]}),flush=True)
            raise RuntimeError('native lifecycle failed, skipped or missing required checks')
        for project in PROJECTS:
            if json.loads(cli('list','--project',project,'--format=json').stdout):
                raise RuntimeError('guest instance cleanup is incomplete')
        fs=os.statvfs(pool_path)
        usage={'pool_available_bytes':fs.f_bavail*fs.f_frsize,'pool_capacity_bytes':fs.f_blocks*fs.f_frsize}
        if usage['pool_available_bytes'] < 2<<30:
            raise RuntimeError('global pool exhaustion cannot count as a root disk quota')
        (report/'storage-after.json').write_text(json.dumps(usage))
        metrics=lifecycle_metrics(events)
        (report/'metrics.json').write_text(json.dumps({'interface':tier['interface'],'metrics':metrics}))
        emit('native_lifecycle_and_project_isolation',interface=tier['interface'],required_test_passes=len(required),metrics=metrics,**usage)
    except Exception:
        # Private diagnostics for a failed run: daemon journal and per-instance
        # LXC/QEMU logs stay in the 0700 report, never on stdout.
        diagnostics=report/'diagnostics'
        diagnostics.mkdir(mode=0o700,exist_ok=True)
        journal=call(['/usr/bin/journalctl','-u','incus.service','-n','400','--no-pager'],check=False,timeout=30)
        (diagnostics/'incusd-journal.log').write_bytes(journal.stdout[-(1<<20):])
        for item in ROOT.glob('diagnostics-*.log'):
            (diagnostics/item.name).write_bytes(item.read_bytes()[-(1<<20):])
        for project in PROJECTS+('default',):
            listed=cli('list','--project',project,'--format=json',check=False)
            if listed.returncode: continue
            for instance in json.loads(listed.stdout or b'[]'):
                if instance['name'] in LAB_INSTANCES+(VM_BUILD,):
                    shown=cli('info',instance['name'],'--project',project,'--show-log',check=False)
                    (diagnostics/(project+'-'+instance['name']+'.log')).write_bytes(shown.stdout[-(1<<20):]+shown.stderr[-(64<<10):])
        raise
    finally:
        # Never destroy unknown objects. This VM was empty, and all names here
        # are fixed fixture inputs. Evidence and failed effects remain on error.
        for project in PROJECTS:
            exists=cli('project','show',project,check=False)
            if exists.returncode: continue
            for instance in json.loads(cli('list','--project',project,'--format=json').stdout):
                if instance['name'] not in LAB_INSTANCES:
                    raise RuntimeError('unexpected lab instance; refusing cleanup')
                cli('delete',instance['name'],'--project',project,'--force')
            for certificate in json.loads(cli('config','trust','list','--format=json').stdout):
                if certificate.get('projects')==[project]: cli('config','trust','remove',certificate['fingerprint'])
            for item in json.loads(cli('image','list','--project',project,'--format=json').stdout):
                if item['fingerprint']!=pin: raise RuntimeError('unexpected image; refusing cleanup')
                cli('image','delete',pin,'--project',project)
            for item in json.loads(cli('profile','list','--project',project,'--format=json').stdout):
                if item['name']!='default':
                    if item['name']!='anas-lease': raise RuntimeError('unexpected profile; refusing cleanup')
                    cli('profile','delete',item['name'],'--project',project)
            cli('project','delete',project)
            bridge='anas'+hashlib.sha256(project.encode()).hexdigest()[:10]
            if cli('network','show',bridge,check=False).returncode==0: cli('network','delete',bridge)
            # The lease source fence ACL shares the bridge name; free it after the bridge.
            if cli('network','acl','show',bridge,check=False).returncode==0: cli('network','acl','delete',bridge)
        # The VM fixture build leaves only fixed names in the default project.
        for instance in json.loads(cli('list','--project','default','--format=json').stdout):
            if instance['name']!=VM_BUILD: raise RuntimeError('unexpected default-project instance; refusing cleanup')
            cli('delete',VM_BUILD,'--force',timeout=180)
        for item in json.loads(cli('image','list','--project','default','--format=json').stdout):
            if not {a.get('name') for a in item.get('aliases') or []} & {VM_BASE_ALIAS,'anas-vmfixture'}:
                raise RuntimeError('unexpected default-project image; refusing cleanup')
            cli('image','delete',item['fingerprint'],timeout=120)
        if cli('storage','show',POOL,check=False).returncode==0: cli('storage','delete',POOL)
        if (PROVIDER_ROOT/'provider').exists():
            (PROVIDER_ROOT/'provider').unlink()
        if PROVIDER_ROOT.exists():
            PROVIDER_ROOT.rmdir()
        if IMAGE_ROOT.exists():
            for item in IMAGE_ROOT.iterdir():
                if not item.name.startswith('vm-fixture') or item.is_symlink() or not item.is_file():
                    raise RuntimeError('unexpected VM fixture file; refusing cleanup')
                item.unlink()
            IMAGE_ROOT.rmdir()
        for certificate in json.loads(cli('config','trust','list','--format=json').stdout):
            if certificate.get('name') in ('anas-lifecycle-manager','anas-lifecycle-manager-next'):
                source = ROOT/('manager-next.crt' if certificate['name'].endswith('-next') else 'manager.crt')
                der = base64.b64decode(b''.join(line for line in source.read_bytes().splitlines() if not line.startswith(b'-----')), validate=True)
                if certificate['fingerprint'] != hashlib.sha256(der).hexdigest():
                    raise RuntimeError('management certificate ownership changed; refusing cleanup')
                cli('config','trust','remove',certificate['fingerprint'])
        if json.loads(cli('config','trust','list','--format=json').stdout):
            raise RuntimeError('lab certificate cleanup is incomplete')
        cli('config','unset','core.https_address')
        emit('owned_lab_resources_removed')


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    for name in ('vm-id','provider','tests','guest-binary','test2json','report-root'): parser.add_argument('--'+name,required=True)
    parser.add_argument('--interface',choices=sorted(TIERS),default='container')
    parser.add_argument('--vm-base-metadata',help='upstream VM image metadata (incus.tar.xz), VM tier only')
    parser.add_argument('--vm-base-disk',help='upstream VM image disk (disk.qcow2), VM tier only')
    try:
        main(parser.parse_args())
    except Exception as exc:
        print(json.dumps({'passed':False,'error_type':type(exc).__name__,
                          'message':str(exc) if type(exc) is RuntimeError else 'private lifecycle harness failed'}),file=sys.stderr)
        sys.exit(1)
