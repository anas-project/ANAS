#!/usr/bin/env python3
"""Real running job -> Core stop -> Hook drain -> disabled account, in a fresh VM.

This is a lifecycle integration fixture, not a complete IAM/PostgreSQL Module
deployment or a release-image build. It runs actual Forgejo, production Hook,
account helper, controller, Provider, Core stop code, Incus and Docker Compose.
Transport images contain measured executables/public OS libraries only. The
fixture SQLite/HTTP proxy and prepared workspace do not stand in for init/render.
"""
import argparse
import base64
from collections import Counter
import hashlib
import http.client
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import sqlite3
import stat
import subprocess
import sys
import tarfile
import time

INPUTS = Path('/opt/anas-forgejo-stop-inputs')
ROOT = Path('/opt/anas-forgejo-stop-native')
WORKSPACE = Path('/srv/anas/native-forgejo-stop')
PROJECT = 'anas-stop-fixture'
POOL = 'anas-btrfs'
BASE = 'https://10.0.2.15:13001'
# policy-loader-r1: independently accepted on the mediated Ubuntu host, with
# exact native image events and the complete workflow matrix. Do not substitute
# a successfully baked but rejected candidate or mutate an older revision.
IMAGE_PIN = '16e440ee8ad764f7faac9bcdaed33388466cfcefd102ffbd0ed1d67982c1d5b1'
EXPORT_SHA = '628ef2b669477823a05bffdc8c8c19e6f963b4a9c94a6328b91afc6f3e34a8e4'
LABEL = 'anas-native:docker://public.ecr.aws/docker/library/busybox@sha256:7a3ebe5bfd1a4a19797d20b0c0bb39d44393e9a03fd852c0865b0f540d868df0'
# Git launches these programs for even a local clone/push. Transporting only
# /usr/bin/git lets Forgejo start but breaks actual repository initialization.
# Preserve fixed official-package programs at their real paths; no credential,
# source checkout, arbitrary host directory or socket enters the image.
TRANSPORT_PROGRAMS = tuple((path, path) for path in (
    '/usr/bin/git', '/usr/bin/git-upload-pack', '/usr/bin/git-receive-pack',
    '/usr/bin/git-upload-archive', '/usr/bin/env', '/usr/bin/cat', '/usr/bin/basename',
    '/usr/bin/dirname', '/bin/sh', '/bin/bash', '/usr/bin/incus',
))
# Fixed read-only diagnostics only. In particular do not probe Podman's socket
# here: an observer must not activate or repair the engine for the controller.
# Raw output stays in the experiment's private directory, outside acceptance
# reports and the public archive. None of these results can satisfy a gate.
GUEST_BOOT_OBSERVATIONS = (
    ('/usr/bin/systemctl','show','--property=ActiveState,SubState,Result,ExecMainStatus,NRestarts',
     'user@1002.service','systemd-logind.service'),
    ('/usr/bin/journalctl','-b','--no-pager','--output=cat','--lines=20','_SYSTEMD_USER_UNIT=anas-podman.service'),
    ('/usr/bin/journalctl','-b','--no-pager','--output=cat','--lines=20','--unit=user@1002.service'),
    ('/usr/bin/stat','-c','%u:%g:%a:%F','/run/anas-podman','/run/anas-podman/podman.sock','/run/user/1002'),
)
REQUIRED = (
    'isolated_vm_and_measured_inputs', 'real_services_and_managed_account',
    'actual_workflow_running', 'core_stop_drains_before_compose_removal',
    'repeated_stop_is_noop', 'disabled_account_after_empty_controller',
    'reenabled_same_account_and_actual_workflow', 'reenabled_core_stop',
    'failed_controller_blocks_core_removal_and_revocation',
    'owned_fixture_cleanup_and_public_reports',
)


class GateFailure(RuntimeError):
    pass


def require(value, code):
    if not value:
        raise GateFailure(code)


def valid_environment(identity, facts):
    return (bool(re.fullmatch(r'anas-incus-host-[a-f0-9]{6}', identity)) and
            facts.get('instance') == identity and facts.get('vendor') == 'QEMU' and
            facts.get('uid') == 0 and facts.get('docker_root') == '/var/lib/anas-host-provision-test' and
            facts.get('containers') == [])


def events_passed(events):
    return (len(events) == len(REQUIRED) and
            Counter(e.get('stage') for e in events) == Counter(REQUIRED) and
            all(e.get('status') == 'passed' for e in events))


def core_events_passed(events, return_code):
    """Require one actual sequential test and its final package result."""
    if type(return_code) is not int or return_code != 0 or not isinstance(events, list):
        return False
    package = 'github.com/anas-project/ANAS/internal/runner'
    name = 'TestNativeForgejoCoreStop'
    started = ran = passed = terminal = False
    for event in events:
        if not isinstance(event, dict) or event.get('Package') != package or terminal:
            return False
        action, test = event.get('Action'), event.get('Test')
        if 'Test' in event and test != name:
            return False
        if action == 'start':
            if test is not None or started or ran:
                return False
            started = True
        elif action == 'run':
            if test != name or ran:
                return False
            ran = True
        elif action == 'pass':
            if test is None:
                if not passed:
                    return False
                terminal = True
            else:
                if not ran or passed:
                    return False
                passed = True
        elif action == 'output':
            if not isinstance(event.get('Output'), str) or (test is not None and (not ran or passed)):
                return False
        else:
            return False
    return ran and passed and terminal


def forwarding_observation(document):
    """Closed diagnostic fields, not a connectivity or authorization proof."""
    if not isinstance(document, dict) or not isinstance(document.get('nftables'), list):
        raise GateFailure('forwarding_observation_invalid')
    matches=[]
    for item in document['nftables']:
        if not isinstance(item,dict):
            raise GateFailure('forwarding_observation_invalid')
        chain=item.get('chain')
        if not isinstance(chain,dict) or (chain.get('family'),chain.get('table'),chain.get('name')) != ('ip','filter','FORWARD'):
            continue
        if chain.get('hook') != 'forward' or chain.get('type') != 'filter' or chain.get('policy') not in ('accept','drop'):
            raise GateFailure('forwarding_observation_invalid')
        matches.append(chain['policy'])
    if len(matches)!=1:
        raise GateFailure('forwarding_observation_ambiguous')
    return {'family':'ip','table':'filter','chain':'FORWARD','policy':matches[0],
            'scope':'observed base policy only; neither connectivity nor complete firewall admission'}


def active_registration_count(database):
    # The fixed Forgejo version retains a numeric deletion timestamp instead
    # of physically removing runner rows. Read only ID/deletion facts from the
    # fixture's read-only connection; retained tombstones are not live runners.
    # Unknown schema, nulls or ambiguous identities are not proof of absence.
    try:
        rows=database.execute('SELECT id,deleted FROM action_runner ORDER BY id LIMIT 1025').fetchall()
    except sqlite3.Error:
        raise GateFailure('registration_inventory_invalid') from None
    require(len(rows)<=1024,'registration_inventory_bound')
    seen=set();active=0
    for identity,deleted in rows:
        require(type(identity) is int and identity>0 and identity not in seen and
                type(deleted) is int and deleted>=0,'registration_inventory_invalid')
        seen.add(identity)
        active+=int(deleted==0)
    return active


def observable_guest(instance):
    if not isinstance(instance,dict) or not isinstance(instance.get('config'),dict):
        return False
    config=instance['config']
    if not all(isinstance(value,str) for value in
               (instance.get('name'),config.get('volatile.uuid'),config.get('user.anas.workload'))):
        return False
    return (bool(re.fullmatch(r'anas-fj-[a-f0-9]{20}',instance.get('name',''))) and
            instance.get('type')=='container' and instance.get('status')=='Running' and
            config.get('user.anas.managed')=='true' and bool(config.get('user.anas.workload')) and
            config.get('volatile.base_image')==IMAGE_PIN and
            bool(re.fullmatch(r'[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}',config.get('volatile.uuid',''))))


def sha256_file(path):
    digest = hashlib.sha256()
    with path.open('rb') as source:
        for block in iter(lambda: source.read(1 << 20), b''):
            digest.update(block)
    return digest.hexdigest()


def new_file(path, body, mode=0o600):
    if isinstance(body, str): body = body.encode()
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
    with os.fdopen(fd, 'wb') as stream:
        os.fchmod(stream.fileno(), mode)
        stream.write(body); stream.flush(); os.fsync(stream.fileno())


def supply_bytes(descriptor):
    # Match the existing compiled Provider protocol. Its canonical decoder
    # intentionally rejects spaces/reordered fields/missing final newline.
    # The nested release retains the original canonical export field order.
    return (json.dumps(descriptor, separators=(',', ':')) + '\n').encode()


def application_configuration(body, enabled):
    active=False; sections=0; flags=0; output=[]
    for line in body.splitlines(keepends=True):
        section=re.fullmatch(r'\s*\[([^\]\r\n]+)\]\s*',line)
        if section:
            active=section[1].lower()=='actions'; sections+=int(active)
        if active and re.match(r'^\s*ENABLED\s*=',line,re.IGNORECASE):
            line,count=re.subn(r'^(\s*ENABLED\s*=\s*)(true|false)(\s*)$',
                lambda match:match[1]+str(enabled).lower()+match[3],line,flags=re.IGNORECASE)
            require(count==1,'invalid_fixture_actions_flag'); flags+=1
        output.append(line)
    require(sections==1 and flags==1,'ambiguous_fixture_actions_flag')
    return ''.join(output)


def workflow_source(delay):
    require(type(delay) is int and delay in (0, 240), 'fixed_workflow_delay')
    return f'''name: native stop lifecycle
on: [push]
jobs:
  smoke:
    runs-on: anas-native
    steps:
      - name: entered-stop-fixture
        shell: sh
        run: |
          test ! -e /run/anas-actions-token/runner-token
          sleep {delay}
          printf 'NATIVE_STOP_PAYLOAD\\n'
'''


def run(identity):
    facts = {'uid': os.geteuid(), 'instance': Path('/var/lib/cloud/data/instance-id').read_text().strip(),
             'vendor': Path('/sys/devices/virtual/dmi/id/sys_vendor').read_text().strip(),
             'docker_root': '/var/lib/anas-host-provision-test', 'containers': []}
    require(valid_environment(identity, facts), 'vm_identity')
    require(not ROOT.exists() and not WORKSPACE.exists(), 'fresh_lifecycle_fixture')
    manifest = json.loads((INPUTS/'source-manifest.json').read_bytes())
    for name, expected in manifest['files'].items():
        path = INPUTS/name
        require(Path(name).name == name and path.is_file() and not path.is_symlink() and
                path.stat().st_uid == 0 and path.stat().st_nlink == 1 and not path.stat().st_mode & 0o022 and
                sha256_file(path) == expected, 'input_identity')
    require(manifest['files']['forgejo'] == 'cb75c2780d13a8a8b91390e42615354219aac58fd86a9d20baa40f5281c8e9a3', 'fixed_forgejo')
    require(sha256_file(INPUTS/'runner-export.tar.gz') == EXPORT_SHA, 'runner_export_digest')
    ROOT.mkdir(mode=0o700); (ROOT/'private').mkdir(); (ROOT/'reports').mkdir()
    WORKSPACE.mkdir(mode=0o700, parents=True)
    prefix = 'fjstop_' + identity[-6:] + '_'
    compose_project = prefix + 'forgejo'
    events, passwords, nginx = [], [], None
    configured_generations=set()
    stage = REQUIRED[0]
    command_env = {'PATH':'/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin', 'HOME':'/root', 'LC_ALL':'C', 'GOPROXY':'off'}

    def command(label, argv, *, payload=None, timeout=90, check=True, env=None):
        require(all(not value or all(value not in str(arg) for arg in argv) for value in passwords), 'secret_in_argv')
        try:
            result = subprocess.run([str(a) for a in argv], input=payload, capture_output=True,
                                    timeout=timeout, env=command_env if env is None else env)
        except subprocess.TimeoutExpired as error:
            new_file(ROOT/'private'/(label+'-timeout-'+secrets.token_hex(3)), (error.stdout or b'') + (error.stderr or b''))
            raise GateFailure('command_timeout') from None
        require(len(result.stdout)+len(result.stderr) <= 8 << 20, 'command_output_bound')
        if check and result.returncode != 0:
            new_file(ROOT/'private'/(label+'-'+secrets.token_hex(3)), result.stdout+b'\n'+result.stderr)
            raise GateFailure('command_failed_'+label)
        return result

    def docker(*argv, **kw): return command('docker', ['/usr/bin/docker', *argv], **kw)
    def incus(*argv, **kw): return command('incus', ['/usr/bin/incus', '--force-local', *argv], **kw)
    def docker_baseline():
        info = json.loads(docker('info','--format','{{json .}}').stdout)
        require(info['DockerRootDir'] == '/var/lib/anas-host-provision-test', 'not_experiment_docker')
        return {'id': info['ID'], 'containers': docker('ps','-aq','--no-trunc').stdout.decode().split(),
                'networks': sorted(docker('network','ls','--no-trunc','--format','{{.ID}}').stdout.decode().split()),
                'volumes': sorted(docker('volume','ls','--format','{{.Name}}').stdout.decode().split()),
                'images': sorted(set(docker('images','-aq','--no-trunc').stdout.decode().split()))}
    baseline = docker_baseline()
    require(baseline['containers'] == [], 'preexisting_container')

    def passed(name, **data):
        event = {'stage':name,'status':'passed',**data}; events.append(event)
        print(json.dumps(event,sort_keys=True),flush=True)

    def api(method, path, username='', password='', data=None):
        conn = http.client.HTTPConnection('127.0.0.1',13000,timeout=10)
        headers = {'Accept':'application/json'}
        if username: headers['Authorization']='Basic '+base64.b64encode((username+':'+password).encode()).decode()
        body = None if data is None else json.dumps(data).encode()
        if body is not None: headers['Content-Type']='application/json'
        try:
            conn.request(method,path,body=body,headers=headers); response=conn.getresponse()
            raw=response.read((2<<20)+1); require(len(raw)<=2<<20,'api_bound')
            return response.status,json.loads(raw) if raw else None
        finally: conn.close()

    def auth_api(method,path,data=None):
        code,body=api(method,path,'admin_forgejo',owner_password,data)
        require(200<=code<300,'fixture_api_status')
        return body

    def account_hook(directory, enabled, label, expect=True):
        request={'abi':'anas.module-hook/v1','module':'forgejo','phase':'local_account_apply','workdir':str(directory),
                 'env':{'CONTAINER_PREFIX':prefix,'FORGEJO_ACTIONS_ENABLED':str(enabled).lower(),
                        'FORGEJO_ACTIONS_CONTROLLER_PASSWORD':controller_password},
                 'secrets':{'candidate':owner_password},'local_account':{'account_id':'break_glass',
                 'handler':'apply-forgejo-break-glass','username':'admin_forgejo','candidate_secret_key':'candidate'}}
        result=command(label,[INPUTS/'hook'],payload=json.dumps(request).encode(),timeout=180,check=expect)
        if not expect: require(result.returncode!=0 and b'Actions account transition' in result.stderr,'unexpected_hook_rejection')

    def compose(directory,*args):
        if args and args[0]=='up' and str(directory) not in configured_generations:
            # The fixture omits upstream's configuration entrypoint. Install
            # the generation input only while this exact project is stopped.
            # Forgejo may persist generated settings in DATA, never in .anas.
            live=docker('ps','-q','--filter','label=com.docker.compose.project='+compose_project).stdout
            require(not live.strip(),'fixture_configuration_requires_stopped_project')
            target=data/'custom/conf/app.ini';info=target.lstat()
            require(stat.S_ISREG(info.st_mode) and info.st_uid==1000 and info.st_nlink==1,'fixture_configuration_owner')
            pending=target.with_name('app.ini.native-pending')
            new_file(pending,(directory/'app.ini').read_bytes(),0o600);os.chown(pending,1000,1000)
            os.replace(pending,target);configured_generations.add(str(directory))
        return docker('compose','--project-name',compose_project,'--project-directory',directory,
                      '--env-file',directory/'.env','-f',directory/'docker-compose.yml',*args,timeout=210)

    def wait_application():
        deadline=time.monotonic()+50
        while time.monotonic()<deadline:
            try:
                code,body=api('GET','/api/v1/version')
                if code==200 and body['version'].split('+')[0]=='15.0.7': return
            except (OSError,http.client.HTTPException): pass
            time.sleep(.3)
        raise GateFailure('forgejo_not_ready')

    def core_stop(case):
        env={**command_env,'ANAS_REQUIRE_FORGEJO_STOP_NATIVE':'1','ANAS_NATIVE_STOP_CASE':case}
        result=command('core-'+case,[INPUTS/'test2json','-t','-p','github.com/anas-project/ANAS/internal/runner',
                        INPUTS/'core.test','-test.v=test2json','-test.count=1','-test.timeout=190s',
                        '-test.run=^TestNativeForgejoCoreStop$'],timeout=200,check=False,env=env)
        new_file(ROOT/'reports'/('core-'+case+'.jsonl'),result.stdout)
        new_file(ROOT/'private'/('core-'+case+'.stderr'),result.stderr)
        rows=[json.loads(line) for line in result.stdout.splitlines()]
        require(core_events_passed(rows,result.returncode),'core_stop_gate')

    def instances():
        rows=json.loads(incus('list','--project',PROJECT,'--format=json').stdout)
        require(isinstance(rows,list),'instance_inventory'); return rows

    boot_samples={}
    def observe_guest_boot(live):
        # A small bounded sample helps explain an entrypoint's exit69 before
        # normal controller compensation deletes the failed guest. This does
        # not suspend cleanup, consume its token, or broaden the exec API.
        if sum(count for count,_ in boot_samples.values())>=8: return
        for instance in live:
            if sum(count for count,_ in boot_samples.values())>=8: return
            if not observable_guest(instance): continue
            identity=(instance['name'],instance['config']['volatile.uuid'])
            count,previous=boot_samples.get(identity,(0,0))
            if count>=2 or time.monotonic()-previous<8: continue
            boot_samples[identity]=(count+1,time.monotonic())
            for index,argv in enumerate(GUEST_BOOT_OBSERVATIONS):
                try:
                    observation=subprocess.run(['/usr/bin/incus','--force-local','exec',identity[0],
                        '--project',PROJECT,'--',*argv],stdin=subprocess.DEVNULL,capture_output=True,
                        timeout=4,env=command_env)
                    body=observation.stdout+observation.stderr
                    diagnostic={'exit':observation.returncode,'truncated':len(body)>32768,
                                'output':body[:32768].decode(errors='replace')}
                except subprocess.TimeoutExpired:
                    diagnostic={'timeout':True}
                new_file(ROOT/'private'/('guest-boot-'+identity[1]+'-'+str(count)+'-'+str(index)+'.json'),
                         json.dumps(diagnostic))

    def state_empty(path):
        state=json.loads(path.read_bytes())
        return state.get('version')==1 and state.get('workloads')=={}

    def database_counts():
        with sqlite3.connect('file:'+str(data/'forgejo.db')+'?mode=ro',uri=True) as db:
            # Non-secret readback only. No token, salt or password hash output.
            return active_registration_count(db)

    def reclaimed(state_path):
        volumes=json.loads(incus('storage','volume','list',POOL,'--project',PROJECT,'--format=json').stdout)
        require(isinstance(volumes,list),'volume_inventory')
        return (state_empty(state_path) and instances()==[] and database_counts()==0 and
                not any(row.get('type') in ('container','virtual-machine') for row in volumes))

    try:
        rules=command('forward-observation',['/usr/sbin/nft','--json','list','chain','ip','filter','FORWARD'],timeout=10)
        observed=forwarding_observation(json.loads(rules.stdout))
        observed['ip_forward']=Path('/proc/sys/net/ipv4/ip_forward').read_text().strip()
        new_file(ROOT/'reports/forwarding-before.json',json.dumps(observed,sort_keys=True)+'\n')
        # Reuse the separately source-bound installed-host fixture. It uses
        # real approval jobs and the default host firewall/control bridge;
        # do not open an additional daemon listener or bypass Docker coexistence.
        host_inputs=Path('/opt/anas-host-action-inputs')
        host_source=json.loads((host_inputs/'source-manifest.json').read_bytes())
        require(sha256_file(host_inputs/'source-manifest.json')==manifest['host_manifest_sha256'] and
                sha256_file(host_inputs/'server-incus-host-action-e2e.py')==host_source['runner_sha256'],'host_fixture_identity')
        spec=importlib.util.spec_from_file_location('stop_installed_host',host_inputs/'server-incus-host-action-e2e.py')
        host=importlib.util.module_from_spec(spec);sys.modules[spec.name]=host;spec.loader.exec_module(host)
        token=host.install_fixture(host_source);host.systemd_identity(host_source)
        console=host.Console();console.enroll(token);host_jobs=[]
        for phase in ('install','configure','enroll'):
            request={'interface':'incus_container','storage_size_gib':24}
            planned=console.wait_job(console.cli('incus-plan','-w','native','--phase',phase,'--request-json',json.dumps(request),'--session-json','-'))
            proof=console.cli('incus-confirm','-w','native','--plan-job',planned['id'],'--action','incus.'+phase,'--session-json','-')
            console.credentials.append(proof['token'])
            envelope={'session':console.envelope(),'plan_job_id':planned['id'],'confirmation_token':proof['token'],'parameters':planned['result']['value']['parameters']}
            applied=console.wait_job(console.cli('incus-apply','-w','native','--phase',phase,'--request-json','-',request=envelope))
            host.validate_phase_result(applied['result']['value'],phase,skip=False);host_jobs.extend([planned['id'],applied['id']])
        bundle=json.loads(Path('/var/lib/anas/incus-host/connection.json').read_bytes())
        require(bundle['storage_pool']==POOL and bundle['architecture']=='amd64','installed_host_target')
        baseline=docker_baseline()
        new_file(ROOT/'reports/host-setup.json',json.dumps({'succeeded_host_jobs':host_jobs,'scope':'installed host approval; runtime baseline begins after its control bridge exists'}))
        passed(stage)
        stage=REQUIRED[1]
        owner_password,controller_password=secrets.token_hex(32),secrets.token_hex(32)
        passwords.extend([owner_password,controller_password])
        data=WORKSPACE/'data'; data.mkdir(mode=0o700); os.chown(data,1000,1000)
        for rel in ('custom','custom/conf','home'):
            (data/rel).mkdir(mode=0o700); os.chown(data/rel,1000,1000)
        state_dir=WORKSPACE/'controller-state';state_dir.mkdir(mode=0o700);os.chown(state_dir,65532,65532)
        state_path=state_dir/'state.json'
        command('public-ca',['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=ANAS native stop CA',
             '-addext','subjectAltName=IP:10.0.2.15','-addext','basicConstraints=critical,CA:TRUE',
             '-addext','keyUsage=critical,digitalSignature,keyCertSign,cRLSign',
             '-keyout',ROOT/'private/tls.key','-out',ROOT/'public-ca.crt'])
        os.chmod(ROOT/'public-ca.crt',0o644)
        app_ini=f'''APP_NAME = ANAS stop integration fixture
RUN_USER = forgejo
RUN_MODE = prod
[database]
DB_TYPE = sqlite3
PATH = /var/lib/gitea/forgejo.db
[repository]
ROOT = /var/lib/gitea/repositories
[server]
PROTOCOL = http
HTTP_ADDR = 0.0.0.0
HTTP_PORT = 3000
ROOT_URL = {BASE}/
DISABLE_SSH = true
OFFLINE_MODE = true
APP_DATA_PATH = /var/lib/gitea/data
[security]
INSTALL_LOCK = true
SECRET_KEY = {secrets.token_hex(32)}
INTERNAL_TOKEN = {secrets.token_hex(64)}
[service]
DISABLE_REGISTRATION = true
ENABLE_BASIC_AUTHENTICATION = true
ENABLE_INTERNAL_SIGNIN = true
[mailer]
ENABLED = false
[actions]
ENABLED = true
[cron]
ENABLED = false
[log]
MODE = console
LEVEL = Error
'''
        new_file(data/'custom/conf/app.ini',app_ini);os.chown(data/'custom/conf/app.ini',1000,1000)

        # A measured binary/OS transport image; not the production Dockerfile.
        # No host credentials, application config, databases or private state
        # are copied into its layers. Library reads remain on this fresh VM.
        archive_path=ROOT/'transport.tar'
        with tarfile.open(archive_path,'w') as tar:
            added=set()
            def file(source,destination):
                name=str(destination).lstrip('/')
                if name in added: return
                added.add(name);source=Path(source)
                body=source.read_bytes(); info=tarfile.TarInfo(name);info.size=len(body);info.mode=0o555
                tar.addfile(info,io.BytesIO(body))
            binaries=[*TRANSPORT_PROGRAMS,
                      (INPUTS/'forgejo','/usr/local/bin/forgejo'),(INPUTS/'helper','/usr/local/bin/anas-forgejo-entrypoint'),
                      (INPUTS/'controller','/usr/local/bin/anas-forgejo-actions-controller')]
            for source,destination in binaries:
                file(source,destination)
                deps=command('ldd',['ldd',source],check=False).stdout.decode()
                for match in re.finditer(r'(/[^\s()]+)',deps):
                    if Path(match.group(1)).is_file(): file(match.group(1),match.group(1))
            for path in Path('/usr/share/git-core/templates').rglob('*'):
                if path.is_file() and not path.is_symlink(): file(path,path)
            for directory,uid,gid,mode in [('/tmp',0,0,0o1777),('/run',0,0,0o755),('/var/lib/gitea',1000,1000,0o700),('/var/lib/anas-actions',65532,65532,0o700)]:
                info=tarfile.TarInfo(directory.lstrip('/'));info.type=tarfile.DIRTYPE;info.uid=uid;info.gid=gid;info.mode=mode;tar.addfile(info)
            for path,body in {'etc/passwd':'root:x:0:0:root:/root:/bin/sh\nforgejo:x:1000:1000:Forgejo:/var/lib/gitea:/bin/sh\ncontroller:x:65532:65532:Controller:/nonexistent:/bin/sh\n',
                              'etc/group':'root:x:0:\nforgejo:x:1000:\ncontroller:x:65532:\n','etc/nsswitch.conf':'hosts: files dns\npasswd: files\ngroup: files\n'}.items():
                info=tarfile.TarInfo(path);raw=body.encode();info.size=len(raw);info.mode=0o444;tar.addfile(info,io.BytesIO(raw))
            file('/etc/ssl/certs/ca-certificates.crt','/etc/ssl/certs/ca-certificates.crt')
        image=docker('import','--change','LABEL dev.anas.native-stop='+identity,archive_path).stdout.decode().strip()
        require(re.fullmatch(r'sha256:[a-f0-9]{64}',image),'transport_image_identity')
        # Exercise the shell utilities used by the actual generated hooks,
        # rather than accepting file presence or a successful Forgejo startup.
        # The old transport's missing basename/cat made hook dispatch silently
        # skip the post-receive event even though the repository API succeeded.
        check=docker('run','--rm','--network','none','--user','1000:1000','--read-only',
                     '--cap-drop','ALL','--security-opt','no-new-privileges',
                     '--entrypoint','/bin/bash',image,'-c',
                     'set -eu; test "$(printf hook-input | cat)" = hook-input; '
                     'test "$(basename /repo/hooks/post-receive)" = post-receive; '
                     'test "$(dirname /repo/hooks/post-receive)" = /repo/hooks; '
                     'test -x /usr/bin/git-upload-pack; test -x /usr/bin/git-receive-pack; '
                     'printf transport-ready')
        require(check.stdout==b'transport-ready','transport_hook_runtime')
        new_file(ROOT/'reports/transport.json',json.dumps({'image':image,'incus_cli_sha256':sha256_file(Path('/usr/bin/incus')),
                  'incus_cli_version':incus('--version').stdout.decode().strip(),'scope':'native binary transport, not packaged release image'}))

        incus('admin','waitready')
        for argv in (('list',),('image','list')):
            require(json.loads(incus(*argv,'--format=json').stdout)==[],'nonempty_test_incus')
        exported=ROOT/'exported';exported.mkdir()
        with tarfile.open(INPUTS/'runner-export.tar.gz','r:gz') as tar:
            members=tar.getmembers()
            require(all(m.isfile() and Path(m.name).name in ('artifact.json','incus.tar.xz','rootfs.squashfs') for m in members) and len(members)==3,'export_members')
            for member in members: new_file(exported/Path(member.name).name,tar.extractfile(member).read())
        release=json.loads((exported/'artifact.json').read_bytes())
        parts=release['artifact']['parts'];combined=hashlib.sha256()
        for name,part in zip(('incus.tar.xz','rootfs.squashfs'),parts):
            path=exported/name;require(sha256_file(path)==part['sha256'] and path.stat().st_size==part['size'],'export_part')
            with path.open('rb') as stream:
                for block in iter(lambda:stream.read(1<<20),b''):combined.update(block)
        require(combined.hexdigest()==IMAGE_PIN,'image_fingerprint')
        supply=Path('/run/anas/compute-image-supply');supply.mkdir(parents=True,mode=0o700)
        for name in ('incus.tar.xz','rootfs.squashfs'):shutil.copy2(exported/name,supply/name);os.chmod(supply/name,0o400)
        descriptor={'version':'anas.compute-image-supply/v1','images':[{'resolution':{'reference':{'fingerprint':IMAGE_PIN},'target':release['artifact']['target'],'fingerprint':IMAGE_PIN},
                 'release':release,'metadata_path':str(supply/'incus.tar.xz'),'rootfs_path':str(supply/'rootfs.squashfs')}]}
        new_file(supply.parent/'compute-image-supply.json',supply_bytes(descriptor),0o400)
        for name in ('consumer',):
            command('mtls',['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=ANAS-stop-'+name,
                         '-keyout',ROOT/'private'/(name+'.key'),'-out',ROOT/'private'/(name+'.crt')])
        enc=lambda path:base64.b64encode(Path(path).read_bytes()).decode()
        endpoint=bundle['endpoint'];server_cert=Path('/var/lib/incus/server.crt')
        provider_env={**command_env,'INCUS_ENDPOINT':endpoint,'INCUS_SERVER_CERT_B64':enc(server_cert),
             'INCUS_ADMIN_CERT_B64':base64.b64encode(bundle['admin_certificate_pem'].encode()).decode(),
             'INCUS_ADMIN_KEY_B64':base64.b64encode(bundle['admin_private_key_pem'].encode()).decode(),
             'ANAS_RESOURCE_CLIENT_CERT':enc(ROOT/'private/consumer.crt'),'INCUS_STORAGE_POOL':POOL,'INCUS_NETWORK_IPV6':'false',
             'ANAS_RESOURCE_CONSUMER':'forgejo','ANAS_RESOURCE_SANDBOX':PROJECT,'ANAS_RESOURCE_INSTANCE_PREFIX':'anas-fj-',
             'ANAS_RESOURCE_IMAGE_ARCHITECTURE':'amd64','ANAS_RESOURCE_MAX_INSTANCES':'1','ANAS_RESOURCE_CPU':'2',
             'ANAS_RESOURCE_MEMORY_MIB':'4096','ANAS_RESOURCE_DISK_GIB':'20','ANAS_RESOURCE_IMAGE_ALLOWLIST':IMAGE_PIN}
        output=command('provider',[INPUTS/'provider','ensure','--isolation','container'],env=provider_env,timeout=300)
        require(json.loads(output.stdout)=={'exists':True,'ready':True,'restricted':True,'quota_enforced':True},'provider_not_ready')
        der=base64.b64decode(b''.join(line for line in server_cert.read_bytes().splitlines() if not line.startswith(b'-----')))
        lease={'INTERFACE':'incus_container','ENDPOINT':endpoint,'SANDBOX':PROJECT,'INSTANCE_PREFIX':'anas-fj-','PROFILE':'anas-lease',
              'SERVER_CERT':enc(server_cert),'SERVER_CERT_FINGERPRINT':hashlib.sha256(der).hexdigest(),
              'CLIENT_CERT':enc(ROOT/'private/consumer.crt'),'CLIENT_KEY':enc(ROOT/'private/consumer.key'),
              'IMAGE_ALLOWLIST':IMAGE_PIN,'MAX_INSTANCES':'1','CPU':'2','MEMORY_MIB':'4096','DISK_GIB':'20'}

        def make_deployment(name,enabled,state=state_dir):
            directory=WORKSPACE/'.anas/deployments'/name/'modules/forgejo';directory.mkdir(parents=True,mode=0o700)
            # The fixture does not use the upstream Docker entrypoint's env
            # conversion. Supply an immutable config per generation instead,
            # so Compose recreates the real server with the same single flag.
            config=application_configuration(app_ini,enabled)
            new_file(directory/'app.ini',config,0o644)
            env={'CONTAINER_PREFIX':prefix,'FORGEJO_ACTIONS_ENABLED':str(enabled).lower(),'IMAGE':image,'DATA':str(data),'STATE':str(state),
                 'CA':str(ROOT/'public-ca.crt'),'FORGEJO_PASSWORD':controller_password,'VM_ID':identity}
            if enabled: env.update({'ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__'+key:value for key,value in lease.items()})
            new_file(directory/'.env',''.join(key+'='+value+'\n' for key,value in env.items()))
            appenv={'PATH':'/usr/local/bin:/usr/bin:/bin','HOME':'/var/lib/gitea/home','USER':'forgejo','FORGEJO_WORK_DIR':'/var/lib/gitea',
                    'FORGEJO_CUSTOM':'/var/lib/gitea/custom','GITEA_WORK_DIR':'/var/lib/gitea','GITEA_CUSTOM':'/var/lib/gitea/custom'}
            controllerenv={'PATH':'/usr/local/bin:/usr/bin:/bin','FORGEJO_ACTIONS_ENABLED':'${FORGEJO_ACTIONS_ENABLED}',
                 'FORGEJO_ACTIONS_STATE_PATH':'/var/lib/anas-actions/state.json','FORGEJO_ALLOWED_SCOPES':'admin_forgejo/onejob',
                 'FORGEJO_URL':'http://anas_forgejo:3000','FORGEJO_RUNNER_URL':BASE+'/', 'FORGEJO_USERNAME':'anas_actions_controller',
                 'FORGEJO_PASSWORD':'${FORGEJO_PASSWORD}','FORGEJO_RUNNER_IMAGE':IMAGE_PIN,'FORGEJO_RUNNER_LABEL':LABEL}
            controllerenv.update({'ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__'+key:'${ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__'+key+':-}' for key in lease})
            services={'anas_forgejo':{'image':'${IMAGE}','container_name':'${CONTAINER_PREFIX}forgejo','entrypoint':['/usr/local/bin/forgejo'],
                 'command':['web','--config','/var/lib/gitea/custom/conf/app.ini','--work-path','/var/lib/gitea'],
                 'user':'1000:1000','read_only':True,'cap_drop':['ALL'],'security_opt':['no-new-privileges:true'],
                 'tmpfs':['/tmp:mode=1777,size=128m'],'environment':appenv,'volumes':['${DATA}:/var/lib/gitea'],
                 'ports':['127.0.0.1:13000:3000'],'labels':{'dev.anas.native-stop':identity}},
                 'anas_forgejo_actions_controller':{'image':'${IMAGE}','container_name':'${CONTAINER_PREFIX}forgejo_actions_controller',
                 'entrypoint':['/usr/local/bin/anas-forgejo-actions-controller'],'command':[], 'user':'65532:65532','read_only':True,
                 'cap_drop':['ALL'],'security_opt':['no-new-privileges:true'],'restart':'no','stop_grace_period':'150s',
                 'tmpfs':['/run:mode=0700,uid=65532,gid=65532,size=16m','/tmp:mode=1777,size=32m'],'environment':controllerenv,
                 'volumes':['${STATE}:/var/lib/anas-actions','${CA}:/etc/ssl/certs/anas-internal-ca.crt:ro'],
                 'networks':{'default':{'gw_priority':1},'compute-control':{}},
                 'labels':{'dev.anas.native-stop':identity},'depends_on':['anas_forgejo']}}
            new_file(directory/'docker-compose.yml',json.dumps({'services':services,'networks':{'compute-control':{'external':True,'name':bundle['control_network']}}}))
            return directory
        enabled_dir=make_deployment('enabled',True)
        compose(enabled_dir,'up','-d','anas_forgejo');wait_application()
        # Keep generated JWT and other settings stable across this fixture's
        # generations; the Actions flag is the only intended config change.
        app_ini=(data/'custom/conf/app.ini').read_text()
        account_hook(enabled_dir,True,'initial-account')
        code,managed=api('GET','/api/v1/user','anas_actions_controller',controller_password)
        require(code==200,'managed_auth');managed_id=managed['id']
        auth_api('POST','/api/v1/user/repos',{'name':'onejob','private':True,'auto_init':True,'default_branch':'main'})
        auth_api('PATCH','/api/v1/repos/admin_forgejo/onejob',{'has_actions':True})
        proxy=f'''worker_processes 1;
pid {ROOT}/private/nginx.pid;
error_log {ROOT}/private/nginx-error.log;
events {{ worker_connections 128; }}
http {{ access_log off; client_body_temp_path {ROOT}/private/client_temp;
  server {{ listen 10.0.2.15:13001 ssl; ssl_certificate {ROOT}/public-ca.crt; ssl_certificate_key {ROOT}/private/tls.key;
    client_max_body_size 16m;
    location / {{ proxy_pass http://127.0.0.1:13000; proxy_http_version 1.1; proxy_set_header Host $http_host;
      proxy_set_header X-Forwarded-Proto https; proxy_buffering off; proxy_request_buffering off; proxy_read_timeout 300s; }}
  }}
}}
'''
        new_file(ROOT/'private/nginx.conf',proxy)
        log=(ROOT/'private/proxy.log').open('xb')
        nginx=subprocess.Popen(['/usr/sbin/nginx','-c',str(ROOT/'private/nginx.conf'),'-g','daemon off;'],env=command_env,stdin=subprocess.DEVNULL,stdout=log,stderr=log)
        compose(enabled_dir,'up','-d');stage=REQUIRED[1];passed(stage,account_id=managed_id)

        revision=None
        def submit(delay):
            nonlocal revision
            workflow=workflow_source(delay)
            body={'branch':'main','message':'Native stop lifecycle','content':base64.b64encode(workflow.encode()).decode()}
            if revision:body['sha']=revision
            result=auth_api('PUT' if revision else 'POST','/api/v1/repos/admin_forgejo/onejob/contents/.forgejo/workflows/stop.yml',body)
            revision=result['content']['sha']; commit=result['commit']['sha'];deadline=time.monotonic()+60
            while time.monotonic()<deadline:
                rows=auth_api('GET','/api/v1/repos/admin_forgejo/onejob/actions/runs')['workflow_runs']
                matches=[row for row in rows if row.get('commit_sha')==commit]
                if len(matches)==1:return matches[0]['id']
                require(len(matches)<=1,'ambiguous_run');time.sleep(1)
            raise GateFailure('workflow_not_queued')

        stage=REQUIRED[2];run_id=submit(240);deadline=time.monotonic()+300
        while time.monotonic()<deadline:
            with sqlite3.connect('file:'+str(data/'forgejo.db')+'?mode=ro',uri=True) as db:
                rows=db.execute('''SELECT s.started,s.stopped FROM action_task_step s
                  JOIN action_task t ON t.id=s.task_id JOIN action_run_job j ON j.id=t.job_id
                  WHERE j.run_id=? AND t.id=j.task_id AND s.name='entered-stop-fixture' ''',(run_id,)).fetchall()
            live=instances()
            if len(rows)==1 and rows[0][0]>0 and rows[0][1]==0 and len(live)==1: break
            observe_guest_boot(live)
            time.sleep(2)
        else:
            output=docker('logs',prefix+'forgejo_actions_controller',check=False)
            new_file(ROOT/'private/controller-start.log',output.stdout+output.stderr)
            raise GateFailure('payload_not_running')
        passed(stage,run_id=run_id)
        stage=REQUIRED[3];core_stop('active')
        require(reclaimed(state_path),'job_cleanup_missing')
        require(docker('ps','-aq').stdout.strip()==b'','containers_remain')
        passed(stage,instances_empty=True,registrations_empty=True,root_disks_empty=True)
        stage=REQUIRED[4];core_stop('repeated');passed(stage)

        stage=REQUIRED[5];disabled_dir=make_deployment('disabled',False)
        compose(disabled_dir,'up','-d');wait_application()
        require(api('GET','/api/v1/user','anas_actions_controller',controller_password)[0]==200,'credential_revoked_before_disabled_reconcile')
        account_hook(disabled_dir,False,'disable-account')
        require(api('GET','/api/v1/user','anas_actions_controller',controller_password)[0] in (401,403),'password_not_revoked')
        require(auth_api('GET','/api/v1/user')['login']=='admin_forgejo','owner_changed');passed(stage)

        stage=REQUIRED[6];core_stop('disabled');reenabled_dir=make_deployment('reenabled',True)
        compose(reenabled_dir,'up','-d');wait_application();account_hook(reenabled_dir,True,'reenable-account')
        code,current=api('GET','/api/v1/user','anas_actions_controller',controller_password)
        require(code==200 and current['id']==managed_id,'account_replaced')
        second=submit(0);deadline=time.monotonic()+300
        while time.monotonic()<deadline:
            rows=auth_api('GET','/api/v1/repos/admin_forgejo/onejob/actions/runs')['workflow_runs']
            current=[row for row in rows if row['id']==second];require(len(current)==1,'run_lost')
            if current[0]['status']=='success' and reclaimed(state_path):break
            require(current[0]['status'] not in ('failure','cancelled','canceled'),'reenabled_workflow_failed');time.sleep(2)
        else:raise GateFailure('reenabled_workflow_timeout')
        # The application is live and Actions is enabled here. Independently
        # corroborate the database's tombstone-aware count through the actual
        # approved-scope API. After Core stops the application that API is
        # intentionally unavailable, so it cannot replace the durable readback.
        registrations=auth_api('GET','/api/v1/repos/admin_forgejo/onejob/actions/runners')
        if isinstance(registrations,dict):registrations=registrations.get('runners')
        require(isinstance(registrations,list) and registrations==[],'runner_api_not_empty')
        passed(stage,run_id=second,account_id=managed_id,active_registration_api_empty=True)
        stage=REQUIRED[7];core_stop('reenabled');require(reclaimed(state_path),'reenabled_cleanup_missing');passed(stage)

        stage=REQUIRED[8]
        failed_state=WORKSPACE/'failed-controller-state';failed_state.mkdir(mode=0o700);os.chown(failed_state,65532,65532)
        new_file(failed_state/'state.json','{"version":1,"workloads":null}');os.chown(failed_state/'state.json',65532,65532)
        before=(failed_state/'state.json').read_bytes()
        failed_dir=make_deployment('failed',False,failed_state);compose(failed_dir,'up','-d');wait_application()
        deadline=time.monotonic()+20
        while time.monotonic()<deadline:
            result=json.loads(docker('inspect','--format','{{json .State}}',prefix+'forgejo_actions_controller').stdout)
            if result['Status']=='exited':break
            time.sleep(.2)
        require(result['Status']=='exited' and result['ExitCode']!=0,'fault_not_reproduced')
        ids=docker('ps','-aq','--no-trunc').stdout
        core_stop('failed');account_hook(failed_dir,False,'failed-disable',expect=False)
        require(docker('ps','-aq','--no-trunc').stdout==ids and (failed_state/'state.json').read_bytes()==before,'failed_evidence_removed')
        require(api('GET','/api/v1/user','anas_actions_controller',controller_password)[0]==200,'uncertain_cleanup_revoked_password')
        passed(stage,failed_state_unchanged=True,containers_retained=True,password_retained=True)

        stage=REQUIRED[9]
        require(instances()==[] and database_counts()==0,'cannot_retire_fixture_with_work')
        for name in (prefix+'forgejo',prefix+'forgejo_actions_controller'):
            label=docker('inspect','--format','{{index .Config.Labels "dev.anas.native-stop"}}',name).stdout.decode().strip()
            require(label==identity,'fixture_ownership_changed')
        # Explicit fixture-owner cleanup AFTER recording the expected rejected
        # product stop. Failed state remains unchanged on the experiment disk;
        # this is not a retry, receipt repair or a passing production cleanup.
        compose(failed_dir,'down');docker('image','rm',image)
        require((failed_state/'state.json').read_bytes()==before,'fault_evidence_changed')
        require(docker_baseline()==baseline,'docker_baseline_changed')
        for path in (ROOT/'reports').iterdir():
            body=path.read_bytes();require(not any(p.encode() in body for p in passwords) and b'PRIVATE KEY-----' not in body,'public_secret')
        passed(stage)
    except Exception as error:
        code=str(error) if isinstance(error,GateFailure) and re.fullmatch(r'[a-z0-9_-]{1,80}',str(error)) else type(error).__name__
        events.append({'stage':stage,'status':'failed','code':code})
    finally:
        if nginx is not None:
            if nginx.poll() is None:nginx.terminate()
            try:nginx.wait(timeout=10)
            except subprocess.TimeoutExpired:nginx.kill();nginx.wait(timeout=5)
    result={'schema':'anas.forgejo-stop-native/v1','vm_id':identity,'passed':events_passed(events),'events':events,
            'scope':'Core stop method + actual production Hook/controller/account helper, real job and Compose; fixture SQLite/workspace/TLS proxy, not complete business stack'}
    new_file(ROOT/'reports/summary.json',json.dumps(result,sort_keys=True,indent=2)+'\n')
    print(json.dumps(result,sort_keys=True),flush=True)
    return 0 if result['passed'] else 1


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--vm-id',required=True);args=parser.parse_args()
    os.umask(0o077)
    try:sys.exit(run(args.vm_id))
    except Exception as error:
        print(json.dumps({'passed':False,'stage':'preparation','code':type(error).__name__}),flush=True);sys.exit(1)
