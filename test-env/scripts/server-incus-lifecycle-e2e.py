#!/usr/bin/env python3
"""Explicit disposable-VM Incus lifecycle test. Never use on a business host.

The exact cloud-init identity must be passed by its owner. Uses a fresh daemon
in that VM, not the physical host daemon. No Docker command is executed.
The rootfs is a tiny measured test fixture, not a distrobuilder product image.
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
PROJECTS = ('anas-lifecycle-a', 'anas-lifecycle-b')
POOL = 'anas-lifecycle-btrfs'


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


def main(args):
    require_vm(args.vm_id)
    for name in ('provider', 'tests', 'guest_binary', 'test2json'):
        p = Path(getattr(args, name))
        if not p.is_absolute() or not p.is_file() or p.is_symlink():
            raise RuntimeError('explicit regular precompiled test inputs required')
    report = Path(args.report_root)
    if not report.is_absolute() or report.exists() or ROOT.exists():
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
    image=report/'fixture.tar.xz'
    pin=create_fixture_image(Path(args.guest_binary),image)
    emit('measured_fixture_image',fingerprint=pin,product_image=False)
    try:
        cli('storage','create',POOL,'btrfs','size=12GiB')
        pool_path=Path('/var/lib/incus/storage-pools')/POOL
        fs=os.statvfs(pool_path)
        if fs.f_bavail*fs.f_frsize < 8<<30:
            raise RuntimeError('quota positive control requires eight GiB free in the test pool')
        emit('real_btrfs_pool_created')
        for name in ('manager','consumer-a','consumer-b'):
            call(['/usr/bin/openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(ROOT/(name+'.key')),
                  '-out',str(ROOT/(name+'.crt')),'-days','1','-subj','/CN=anas-lifecycle-'+name],timeout=20)
        cli('config','trust','add-certificate',str(ROOT/'manager.crt'),'--name=anas-lifecycle-manager')
        cli('config','set','core.https_address','127.0.0.1:8443')
        server=Path('/var/lib/incus/server.crt').read_bytes()
        server_der=base64.b64decode(b''.join(x for x in server.splitlines() if not x.startswith(b'-----')))
        encode=lambda p:base64.b64encode(p.read_bytes()).decode()
        leases=[]
        for project,name in zip(PROJECTS,('consumer-a','consumer-b')):
            # Preload real measured image bytes before ensure, without using a
            # fake allowlist or inventing a Provider result/supply protocol.
            cli('project','create',project,'-c','features.networks=false','-c','features.images=true','-c','features.profiles=true')
            cli('image','import',str(image),'--project',project)
            provider_env={'PATH':env['PATH'],'INCUS_ENDPOINT':'https://127.0.0.1:8443','INCUS_SERVER_CERT_B64':base64.b64encode(server).decode(),
                          'INCUS_ADMIN_CERT_B64':encode(ROOT/'manager.crt'),'INCUS_ADMIN_KEY_B64':encode(ROOT/'manager.key'),
                          'ANAS_RESOURCE_CLIENT_CERT':encode(ROOT/(name+'.crt')),'INCUS_STORAGE_POOL':POOL,
                          'INCUS_NETWORK_IPV6':'false','ANAS_RESOURCE_CONSUMER':name.replace('-','_'),
                          'ANAS_RESOURCE_SANDBOX':project,'ANAS_RESOURCE_INSTANCE_PREFIX':'anas-native-',
                          'ANAS_RESOURCE_IMAGE_ARCHITECTURE':'amd64','ANAS_RESOURCE_MAX_INSTANCES':'1',
                          'ANAS_RESOURCE_CPU':'1','ANAS_RESOURCE_MEMORY_MIB':'512','ANAS_RESOURCE_DISK_GIB':'4','ANAS_RESOURCE_IMAGE_ALLOWLIST':pin}
            for operation in ('ensure','ensure','inspect'):
                result=call([args.provider,operation,'--isolation','container'],provider_env,check=False)
                if result.returncode:
                    (report/(project+'-'+operation+'-failure.txt')).write_bytes(result.stderr)
                    raise RuntimeError('real Provider '+operation+' failed; private diagnostic retained')
                if json.loads(result.stdout) != {'exists':True,'ready':True,'restricted':True,'quota_enforced':True}:
                    raise RuntimeError('Provider did not confirm complete lease')
            emit('provider_ensure_repeat_inspect',project=project)
            leases.append({'Interface':'incus_container','Endpoint':'https://127.0.0.1:8443','Sandbox':project,
                           'InstancePrefix':'anas-native-','Profile':'anas-lease','ServerCertFingerprint':hashlib.sha256(server_der).hexdigest(),
                           'ServerCertB64':base64.b64encode(server).decode(),'ClientCertB64':encode(ROOT/(name+'.crt')),
                           'ClientKeyB64':encode(ROOT/(name+'.key')),'ImageAllowlist':[pin],'MaxInstances':1,'CPU':1,'MemoryMiB':512,'DiskGiB':4})
        (ROOT/'leases.json').write_text(json.dumps(leases))
        package='github.com/anas-project/ANAS/internal/computeclient'
        with (report/'lifecycle.jsonl').open('x') as output:
            result=subprocess.run([args.test2json,'-t','-p',package,args.tests,'-test.v=test2json','-test.count=1',
                                   '-test.timeout=9m','-test.run=^TestNativeIncusContainerLeaseLifecycle$'],
                                  env={'PATH':env['PATH'],'GOMAXPROCS':'1','ANAS_REQUIRE_INCUS_LIFECYCLE_NATIVE':'1'},
                                  stdin=subprocess.DEVNULL,stdout=output,stderr=subprocess.STDOUT,timeout=570)
        events=[json.loads(line) for line in (report/'lifecycle.jsonl').read_text().splitlines()]
        required={'TestNativeIncusContainerLeaseLifecycle',*('TestNativeIncusContainerLeaseLifecycle/'+x for x in
                   ('same-name-project-isolation','daemon-instance-quota','daemon-rejects-direct-quota-and-device-overrides','stdin-secret','btrfs-root-disk-quota','exec-cancel-and-independent-reclaim','stop-delete-idempotent')),
                  *('TestNativeIncusContainerLeaseLifecycle/daemon-rejects-direct-quota-and-device-overrides/'+x for x in ('cpu','memory','disk','host-disk','other-project-network'))}
        passed={e.get('Test') for e in events if e.get('Action')=='pass'}
        if result.returncode or any(e.get('Action') in ('fail','skip') for e in events) or not required.issubset(passed) or not any(e.get('Action')=='pass' and not e.get('Test') for e in events):
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
        metrics=[]
        for event in events:
            match=re.search(r'(lease_[01]_create_start_ready_ms=\d+|actual_guest_write_bytes=\d+ limit_bytes=\d+)',event.get('Output',''))
            if match:metrics.append(match.group(1))
        emit('native_lifecycle_and_project_isolation',required_test_passes=len(required),metrics=metrics,**usage)
    finally:
        # Never destroy unknown objects. This VM was empty, and all names here
        # are fixed fixture inputs. Evidence and failed effects remain on error.
        for project in PROJECTS:
            exists=cli('project','show',project,check=False)
            if exists.returncode: continue
            for instance in json.loads(cli('list','--project',project,'--format=json').stdout):
                if instance['name'] not in ('anas-native-job','anas-native-overquota'):
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
        if cli('storage','show',POOL,check=False).returncode==0: cli('storage','delete',POOL)
        for certificate in json.loads(cli('config','trust','list','--format=json').stdout):
            if certificate.get('name')=='anas-lifecycle-manager': cli('config','trust','remove',certificate['fingerprint'])
        cli('config','unset','core.https_address')
        emit('owned_lab_resources_removed')


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    for name in ('vm-id','provider','tests','guest-binary','test2json','report-root'): parser.add_argument('--'+name,required=True)
    try:
        main(parser.parse_args())
    except Exception as exc:
        print(json.dumps({'passed':False,'error_type':type(exc).__name__,
                          'message':str(exc) if type(exc) is RuntimeError else 'private lifecycle harness failed'}),file=sys.stderr)
        sys.exit(1)
