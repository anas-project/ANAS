#!/usr/bin/env python3
"""Actual CLI single Actions switch in its exact disposable QEMU VM.

Runtime configuration, command output and credentials remain in the private VM
root. Only finite verdicts, counts and public build metadata enter the report.
"""
import argparse,base64,configparser,gzip,hashlib,http.client,importlib.util,json,os,re,shutil,socket,ssl,stat,subprocess,tarfile,threading,time,traceback,urllib.error,urllib.request
from pathlib import Path
import yaml
INPUTS=Path('/opt/anas-lifecycle-inputs')
PRIVATE=Path('/var/lib/anas-forgejo-core-test')
REPORT=Path('/opt/anas-lifecycle-report-core')
SOCKET='/run/anas-forgejo-core-docker.sock'
BASE='https://git.fj-core.test:19000'
PREFIX='fjcore_'
PROJECT='anas-forgejo-runners'
POOL='anas-core-btrfs'
SOURCE_SHA='6fb28f7d2d6efea874eac504c56df16041be71e2011a0aa3c79cddf50b0c2a72'
SOURCE_ID='1f287f49f98e996b90184dd6ec383c22bd6a89e4c7aec78b11dde9ff0f10c870'
IMAGE_SHA='1a8958fe695e56433239142a68ca3433556c1e2ffdfffe58bb1f1c5a2dfe48f9'
CONTROLLER_SHA='5d845f9c758ee70f130d559548edb3cce7d29e7c2f783dd169577adff132b3d3'
RUNNER_SHA='cc1aac93bb18d0c8624438a349190757cfb13d1567fe9d6e67db2024006924c7'
FINGERPRINT='3e03737b6bb78b96b82b7a5036919a23a5ca26620a09b504451144933db6e940'
DOCKERFILE_SHA='34da615ad3dc8678ba14bf7988c82bf2cc23e4bed1f0790bd07508de27145cda'
CONTROLLER_REF='ghcr.io/anas-project/anas-forgejo-actions-controller:17.0.1-r1'
PROVIDER_REF='ghcr.io/anas-project/anas-incus-provisioner:7.3.0-r4'

def sha(path):
 h=hashlib.sha256()
 with Path(path).open('rb') as f:
  for data in iter(lambda:f.read(1<<20),b''):h.update(data)
 return h.hexdigest()

def unpack(path,dest,allowed=None,limit=400<<20):
 assert not dest.exists();dest.mkdir(mode=0o700)
 with tarfile.open(path) as t:
  items=t.getmembers();assert sum(m.size for m in items)<=limit
  for m in items:
   p=Path(m.name);assert not p.is_absolute() and '..' not in p.parts and (m.isdir() or m.isfile())
   if allowed is not None:assert p.parts[0] in allowed
   target=dest/p
   if m.isdir():target.mkdir(parents=True,exist_ok=True,mode=0o755)
   else:
    target.parent.mkdir(parents=True,exist_ok=True,mode=0o755)
    with t.extractfile(m) as src,target.open('xb') as out:shutil.copyfileobj(src,out)
    target.chmod(0o755 if m.mode&0o111 else 0o644)

def incus_get(path):
 c=http.client.HTTPConnection('localhost',timeout=8);c.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);c.sock.settimeout(8);c.sock.connect('/var/lib/incus/unix.socket')
 try:
  c.request('GET',path);response=c.getresponse();body=json.loads(response.read(4<<20));assert response.status==200 and body.get('type')=='sync';return body['metadata']
 finally:c.close()

def inventories():
 assert incus_get('/1.0/projects/'+PROJECT)['name']==PROJECT
 instances=incus_get('/1.0/instances?recursion=1&project='+PROJECT)
 volumes=incus_get('/1.0/storage-pools/'+POOL+'/volumes?recursion=1&project='+PROJECT)
 assert isinstance(instances,list) and isinstance(volumes,list)
 return {'instances':len(instances),'root_disks':sum(v['type'] in ('container','virtual-machine') for v in volumes)}

def main(args):
 resuming=args.resume_prepared or args.resume_standard
 assert args.samba_full_image is None or (args.resume_standard and re.fullmatch(r'sha256:[a-f0-9]{64}',args.samba_full_image))
 assert os.geteuid()==0 and re.fullmatch(r'anas-runner-bake-[a-f0-9]{6}',args.vm_id)
 assert Path('/var/lib/cloud/data/instance-id').read_text()==args.vm_id+'\n'
 assert Path('/sys/class/dmi/id/sys_vendor').read_text().strip()=='QEMU'
 assert PRIVATE.exists()==resuming and not REPORT.exists() and not Path(SOCKET).exists()
 if args.resume_prepared:
  prior=json.loads(Path('/opt/anas-lifecycle-report-core/summary.json').read_text());assert prior['stage']=='initial_disabled_apply' and prior['error_type']=='AssertionError' and prior['private_command_count']==35
  assert any(x['check']=='actual_security_controller_and_incus_cli_elf_match' and x['passed'] is True for x in prior['checks'])
  assert json.loads(Path('/opt/anas-lifecycle-report-core/source-baseline.json').read_text())['base_source']==SOURCE_ID
 if args.resume_standard:
  assert args.standard_host
  prior=json.loads(Path('/opt/anas-lifecycle-report-core/summary.json').read_text());assert prior['stage']=='enabled_workflow' and prior['error_type']=='AssertionError'
  assert any(x['check']=='one_actions_switch_enables_server_and_controller' and x['passed'] is True for x in prior['checks'])
  assert any(x['check']=='standard_installed_host_and_automatic_connection' and x['passed'] is True for x in prior['checks'])
  for item in json.loads(Path('/opt/anas-lifecycle-report-core/samba-dns-overlay.json').read_text())['source_overlays']:
   assert sha(PRIVATE/'source'/item['path'])==item['sha256']
 for name,digest in [('core-source-and-binaries.tar.gz',SOURCE_SHA),('core-public-formal-images.tar.gz',IMAGE_SHA),('core-controller-security-image.tar.gz',CONTROLLER_SHA),('runner-export.tar.gz',RUNNER_SHA),('incus-provider.Dockerfile',DOCKERFILE_SHA)]:
  p=INPUTS/name;info=p.lstat();assert stat.S_ISREG(info.st_mode) and info.st_uid==0 and info.st_nlink==1 and not info.st_mode&0o022 and sha(p)==digest,name
 if args.dns_fix:
  assert args.resume_prepared or args.standard_host
  for name,digest in [('samba-12-bind.sh','3fe98cae2b83a41c8544d5168341533a0a53fd04368fc4f61f6053c4eb49de8c'),('samba-named.conf.envsubst','7c999d634b9342826e8423af4ff9b57e9afb85d0a8e1d96e5613bbb3c6e79bfa')]:
   info=(INPUTS/name).lstat();assert stat.S_ISREG(info.st_mode) and info.st_uid==0 and not info.st_mode&0o022 and sha(INPUTS/name)==digest
 if args.standard_host:
  p=INPUTS/'core-host-inputs-20261101.tar.gz';assert sha(p)=='1087ed1be993666239147914f35079bdfe247af064be7fede663fb377038cb75'
 os.umask(0o077);PRIVATE.mkdir(mode=0o700,exist_ok=resuming);REPORT.mkdir(mode=0o700)
 logs=PRIVATE/('commands-standard-resume' if args.resume_standard else 'commands-continued' if resuming else 'commands');logs.mkdir(mode=0o700)
 env={'PATH':'/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin','HOME':str(PRIVATE),'DOCKER_HOST':'unix://'+SOCKET,'DOCKER_CONFIG':str(PRIVATE/'docker-client'),'LC_ALL':'C.UTF-8','ANAS_SHARED_BUILD_CONTEXT':str(PRIVATE/'source')}
 (PRIVATE/'docker-client').mkdir(mode=0o700,exist_ok=resuming)
 results=list(prior['checks']) if resuming else [];counter=0;docker=None;stage='prepare'
 def record(name,**data):
  results.append({'check':name,'passed':True,**data});(REPORT/'checks.json').write_text(json.dumps(results));print(json.dumps(results[-1]),flush=True)
 def run(argv,timeout=180,data=None,check=True):
  nonlocal counter
  counter+=1
  p=subprocess.run([str(x) for x in argv],env=env,input=data,capture_output=True,timeout=timeout)
  (logs/('%04d.stdout'%counter)).write_bytes(p.stdout);(logs/('%04d.stderr'%counter)).write_bytes(p.stderr)
  if check and p.returncode:
   failure_status=REPORT/'failed-command-status.json'
   if not failure_status.exists():failure_status.write_text(json.dumps({'index':counter,'program':Path(argv[0]).name,'returncode':p.returncode,'stage':stage}))
   raise RuntimeError('fixed operation failed: '+Path(argv[0]).name+'; private command '+str(counter))
  return p
 def d(*argv,**kw):return run(['docker','-H','unix://'+SOCKET,*argv],**kw)
 def cli(*argv,**kw):
  if argv[:2]==('config','set'):argv=('config','set','--root',str(PRIVATE/'source/modules'),'--defer','--update-lock',*argv[2:])
  return run([Path('/usr/local/bin/anas') if args.standard_host else PRIVATE/'inputs/anas',*argv],**kw)
 def inc(*argv,**kw):return run(['incus','--force-local',*argv],**kw)
 workspace=Path('/srv/anas/core-actions') if args.standard_host else PRIVATE/'workspace'
 try:
  if not resuming:
   stage='source';unpack(INPUTS/'core-source-and-binaries.tar.gz',PRIVATE/'staged',{'source','inputs','source-manifest.json','build-results.json'})
   staged=PRIVATE/'staged';manifest=json.loads((staged/'source-manifest.json').read_text());assert manifest['source_identity']==SOURCE_ID
   for item in manifest['files']:assert sha(staged/'source'/item['path'])==item['sha256']
   shutil.move(str(staged/'source'),str(PRIVATE/'source'));shutil.move(str(staged/'inputs'),str(PRIVATE/'inputs'))
   builds=json.loads((staged/'build-results.json').read_text())
   for item in builds['results']:
    assert item['exit_code']==0
    path=PRIVATE/'inputs/anas' if item['program']=='anas' else PRIVATE/'source/modules'/item['program'][:-5]/'hook/bin/linux-amd64/anas-hook'
    assert sha(path)==item['sha256']
   shutil.copyfile(INPUTS/'incus-provider.Dockerfile',PRIVATE/'source/modules/incus/provisioner/Dockerfile')
   (REPORT/'source-baseline.json').write_text(json.dumps({'base_source':SOURCE_ID,'provider_dockerfile_overlay':DOCKERFILE_SHA,'programs':builds['results'],'runner_fingerprint':FINGERPRINT},indent=2))
   record('frozen_source_and_actual_product_programs',source_identity=SOURCE_ID)
  stage='docker';assert not run(['systemctl','is-active','docker.service'],check=False).returncode==0
  daemon={'data-root':str(PRIVATE/'docker'),'exec-root':str(PRIVATE/'docker-exec'),'pidfile':str(PRIVATE/'docker.pid'),'hosts':['unix://'+SOCKET]}
  if args.standard_host:
   assert not Path('/run/docker.sock').exists();daemon['hosts'].append('unix:///run/docker.sock')
  (PRIVATE/'daemon.json').write_text(json.dumps(daemon))
  if resuming:
   existing={x['local'] for row in json.loads(run(['ip','-j','address','show','dev','lo']).stdout) for x in row.get('addr_info',[])}
   for address in ['10.253.31.2','10.253.31.249','10.253.31.250']:
    if address not in existing:run(['ip','address','add',address+'/32','dev','lo'])
   if not args.resume_standard:inc('admin','waitready')
  daemonlog=(PRIVATE/('docker-continued-private.log' if args.resume_prepared else 'docker-private.log')).open('wb');docker=subprocess.Popen(['dockerd','--config-file',str(PRIVATE/'daemon.json')],env=env,stdout=daemonlog,stderr=daemonlog)
  deadline=time.monotonic()+90
  while True:
   assert docker.poll() is None
   try:p=d('info','--format','{{.DockerRootDir}}',timeout=10,check=False)
   except subprocess.TimeoutExpired:
    if time.monotonic()>deadline:raise RuntimeError('owned Docker readiness timeout')
    time.sleep(.5);continue
   if p.returncode==0:assert p.stdout.decode().strip()==str(PRIVATE/'docker');break
   if time.monotonic()>deadline:raise RuntimeError('owned Docker readiness timeout')
   time.sleep(.5)
  if not resuming:
   assert not d('ps','-aq').stdout.strip();record('fresh_owned_docker')
  else:
   names=d('ps','-a','--format','{{.Names}}').stdout.decode().splitlines();assert all(x.startswith(PREFIX) for x in names)
   if args.resume_standard:
    run(['systemctl','restart','anas-incus-network.service'],timeout=90);run(['systemctl','restart','incus.service'],timeout=90);inc('admin','waitready');run(['systemctl','start','anas-hostd.socket','anasd.service'],timeout=60)
   record('resumed_same_private_test_workspace',host_docker_used=False)
   shutil.copyfile('/opt/anas-lifecycle-report-core/source-baseline.json',REPORT/'source-baseline.json')
   public_images=json.loads(Path('/opt/anas-lifecycle-report-core/product-images.json').read_text());(REPORT/'product-images.json').write_text(json.dumps(public_images))
  if not resuming:
   stage='images';d('image','load','-i',str(INPUTS/'core-public-formal-images.tar.gz'),timeout=600);d('image','load','-i',str(INPUTS/'core-controller-security-image.tar.gz'),timeout=180)
   with tarfile.open(INPUTS/'core-controller-security-image.tar.gz') as t:
    metadata=json.load(t.extractfile('manifest.json'));assert len(metadata)==1
    tags=metadata[0]['RepoTags'];assert tags==['anas-forgejo-controller-amd64-security:20261030']
   d('image','tag',tags[0],CONTROLLER_REF)
   module=PRIVATE/'source/modules/incus/provisioner'
   d('buildx','build','--load','--platform','linux/amd64','--build-context','shared='+str(PRIVATE/'source'),'--build-arg','DOCKER_HUB_REGISTRY=public.ecr.aws/docker','--build-arg','GO_BUILDER_REGISTRY=public.ecr.aws/docker','--metadata-file',str(REPORT/'provider-build-metadata.json'),'-f',str(module/'Dockerfile'),'-t',PROVIDER_REF,str(module),timeout=900)
   public_images=[]
   for ref in [CONTROLLER_REF,PROVIDER_REF,'ghcr.io/anas-project/anas-forgejo:17.0.1-r1']:
    view=json.loads(d('image','inspect','--format','{"id":{{json .Id}},"digests":{{json .RepoDigests}},"architecture":{{json .Architecture}}}',ref).stdout);assert view['architecture']=='amd64';public_images.append({'ref':ref,**view})
   (REPORT/'product-images.json').write_text(json.dumps(public_images,indent=2));record('formal_provider_build_and_security_controller')
   stage='image_bundle';unpack(INPUTS/'runner-export.tar.gz',PRIVATE/'runner-export',{'artifact.json','incus.tar.xz','rootfs.squashfs'},300<<20)
   artifact=INPUTS/'incus-image-artifacts-current';archive=PRIVATE/'image-archive'
   run([artifact,'init','--archive',archive])
   run([artifact,'record','--archive',archive,'--name','forgejo-runner','--revision','native-20261031-r2','--architecture','amd64','--interface','incus_container','--recipe',INPUTS/'runner-current.yml','--format','split','--metadata',PRIVATE/'runner-export/incus.tar.xz','--rootfs',PRIVATE/'runner-export/rootfs.squashfs'])
   run([artifact,'bundle','--archive',archive,'--first-release','--output-dir',PRIVATE/'image-bundle'])
   catalog=PRIVATE/'source/modules/incus/images';catalog.mkdir(exist_ok=True)
   old=catalog/'catalog.json'
   if old.exists():assert json.loads(old.read_text())==[];old.unlink()
   for p in (PRIVATE/'image-bundle').iterdir():
    target=catalog/p.name;assert not target.exists()
    if p.is_dir():shutil.copytree(p,target)
    else:shutil.copyfile(p,target)
   record('private_experimental_provider_bundle',published=False)
   if not args.standard_host:
    stage='incus';inc('admin','waitready');assert incus_get('/1.0/projects?recursion=1')
    assert len(json.loads(inc('list','--format=json').stdout))==0 and len(json.loads(inc('storage','list','--format=json').stdout))==0
    inc('storage','create',POOL,'btrfs','size=40GiB')
    run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=ANAS-Core-fixture-manager','-keyout',PRIVATE/'manager.key','-out',PRIVATE/'manager.crt'])
    inc('config','trust','add-certificate',PRIVATE/'manager.crt','--name=anas-core-fixture-manager');inc('config','set','core.https_address','0.0.0.0:8443')
   for address in ['10.253.31.2','10.253.31.249','10.253.31.250']:run(['ip','address','add',address+'/32','dev','lo'])
   names=['git.fj-core.test','auth.fj-core.test','dc.fj-core.test','fj-core.test']
   with Path('/etc/hosts').open('a') as f:f.write('\n10.253.31.2 '+' '.join(names)+'\n')
   enc=lambda p:base64.b64encode(Path(p).read_bytes()).decode()
   config={'modules':{'samba_dc':{'config':{'domain':'fj-core.test','netbios_name':'FJCORE'}},'postgres':{},'forgejo':{'config':{'db_type':'postgres','ssh_port':2222,'actions_enabled':False,'actions_runner_image':{'catalog':'anas','name':'forgejo-runner','revision':'native-20261031-r2'}}},'incus':{'config':({} if args.standard_host else {'image_architecture':'amd64','storage_pool':POOL,'endpoint':'https://10.253.31.2:8443','server_certificate_b64':enc('/var/lib/incus/server.crt'),'admin_certificate_b64':enc(PRIVATE/'manager.crt'),'admin_key_b64':enc(PRIVATE/'manager.key')})},'traefik':{'config':{'base_port':19000}}},'identity':{'iam':{'provider':'casdoor'}},'global':{'base_domain':'fj-core.test','email':'admin@fj-core.test','timezone':'Asia/Singapore','default_language':'en-US','virtual_domain':True,'container_prefix':PREFIX,'network_prefix':PREFIX,'host_ip':'10.253.31.2','host_lan_ip':'10.253.31.250','host_lan_bridge_ip':'10.253.31.249'}}
   if args.standard_host:config['modules'].pop('incus')
   configfile=PRIVATE/'initial-config.yml';configfile.write_text(yaml.safe_dump(config));configfile.chmod(0o600)
   if not args.standard_host:
    cli('init','-c',configfile,'-module-root',PRIVATE/'source/modules','-y','-json',workspace,timeout=600)
   else:
    stage='standard_host';host_inputs=Path('/opt/anas-host-action-inputs')
    unpack(INPUTS/'core-host-inputs-20261101.tar.gz',host_inputs,limit=120<<20)
    host_source=json.loads((host_inputs/'source-manifest.json').read_text());assert host_source['source_identity']==SOURCE_ID
    assert sha(host_inputs/'server-incus-host-action-e2e.py')==host_source['runner_sha256']
    (REPORT/'host-input-manifest.json').write_text(json.dumps(host_source));base=json.loads((REPORT/'source-baseline.json').read_text());base['actual_core_cli']={'path':'/usr/local/bin/anas','sha256':host_source['artifacts']['anas'],'release':host_source['release']};(REPORT/'source-baseline.json').write_text(json.dumps(base))
    spec=importlib.util.spec_from_file_location('core_standard_host_fixture',host_inputs/'server-incus-host-action-e2e.py');host=importlib.util.module_from_spec(spec);spec.loader.exec_module(host)
    token=host.install_fixture(host_source,workspaces=(('native',str(workspace)),),prepare_workspace=lambda target:cli('init','-c',configfile,'-module-root',PRIVATE/'source/modules','-y','-json',target,timeout=600))
    host_identity=host.systemd_identity(host_source);console=host.Console();console.enroll(token);host_jobs=[]
    for phase in ('install','configure','enroll'):
     request={'interface':'incus_container','storage_size_gib':24,'chinese_speedup':True}
     planned=console.wait_job(console.cli('incus-plan','-w','native','--phase',phase,'--request-json',json.dumps(request),'--session-json','-'))
     proof=console.cli('incus-confirm','-w','native','--plan-job',planned['id'],'--action','incus.'+phase,'--session-json','-');console.credentials.append(proof['token'])
     envelope={'session':console.envelope(),'plan_job_id':planned['id'],'confirmation_token':proof['token'],'parameters':planned['result']['value']['parameters']}
     applied=console.wait_job(console.cli('incus-apply','-w','native','--phase',phase,'--request-json','-',request=envelope))
     host.validate_phase_result(applied['result']['value'],phase,skip=False);host_jobs.append({'phase':phase,'plan_job':planned['id'],'apply_job':applied['id']})
     (REPORT/'standard-host-jobs.json').write_text(json.dumps(host_jobs));record('standard_host_'+phase,job_id=applied['id'])
    bundle=json.loads(Path('/var/lib/anas/incus-host/connection.json').read_text());assert bundle['storage_pool']==POOL and bundle['architecture']=='amd64'
    base=json.loads((REPORT/'source-baseline.json').read_text());base['host_product_artifacts']=host_source;base['actual_core_cli']={'path':'/usr/local/bin/anas','sha256':host_source['artifacts']['anas'],'release':host_source['release']};(REPORT/'source-baseline.json').write_text(json.dumps(base,indent=2))
    (REPORT/'host-service-identity.json').write_text(json.dumps(host_identity));record('standard_installed_host_and_automatic_connection')

  if args.dns_fix:
   stage='samba_dns_fix';module=PRIVATE/'source/modules/samba_dc';overlay=[]
   for name,rel in [('samba-12-bind.sh','samba_dc/root/etc/cont-init.d/12-bind.sh'),('samba-named.conf.envsubst','samba_dc/root/etc/bind/named.conf.envsubst')]:
    target=module/rel;shutil.copyfile(INPUTS/name,target);overlay.append({'path':'modules/samba_dc/'+rel,'sha256':sha(target)})
   for name,key in [('module.yml','revision'),('localization.yml','module_revision')]:
    target=module/name;text,count=re.subn(r'(?m)^'+key+r': 13$',key+': 14',target.read_text());assert count==1;target.write_text(text);overlay.append({'path':'modules/samba_dc/'+name,'sha256':sha(target)})
   for target in module.rglob('*'):
    if target.is_file() and target.suffix in ('.yml','.md'):
     text=target.read_text()
     if '4.23.6-r13' in text:
      target.write_text(text.replace('4.23.6-r13','4.23.6-r14'));overlay.append({'path':str(target.relative_to(PRIVATE/'source')),'sha256':sha(target)})
   context=PRIVATE/'samba-dns-build';context.mkdir(mode=0o700)
   shutil.copyfile(INPUTS/'samba-12-bind.sh',context/'12-bind.sh');shutil.copyfile(INPUTS/'samba-named.conf.envsubst',context/'named.conf.envsubst')
   base='ghcr.io/anas-project/anas-samba-dc:4.23.6-r13';fixed='ghcr.io/anas-project/anas-samba-dc:4.23.6-r14'
   base_id=d('image','inspect','--format','{{.Id}}',base).stdout.decode().strip()
   dockerfile='FROM '+base+'\nCOPY 12-bind.sh /etc/cont-init.d/12-bind.sh\nCOPY named.conf.envsubst /etc/bind/named.conf.envsubst\nRUN chmod 0755 /etc/cont-init.d/12-bind.sh && chmod 0644 /etc/bind/named.conf.envsubst\n'
   (context/'Dockerfile').write_text(dockerfile)
   d('buildx','build','--load','--pull=false','--provenance=false','--metadata-file',str(REPORT/'samba-dns-build-metadata.json'),'-t',fixed,str(context),timeout=300)
   d('image','tag','ghcr.io/anas-project/anas-samba-dc-anchor:4.23.6-r13','ghcr.io/anas-project/anas-samba-dc-anchor:4.23.6-r14')
   fixed_id=d('image','inspect','--format','{{.Id}}',fixed).stdout.decode().strip()
   receipt={'experimental':True,'published':False,'e2e_revision':14,'base_ref':base,'base_id':base_id,'fixed_ref':fixed,'fixed_id':fixed_id,'source_overlays':overlay,'dockerfile_sha256':sha(context/'Dockerfile'),'construction':'public r13 base plus two text runtime assets; unchanged anchor retagged locally; not a full original Dockerfile rebuild'}
   (REPORT/'samba-dns-overlay.json').write_text(json.dumps(receipt,indent=2));shutil.copyfile(context/'Dockerfile',REPORT/'samba-dns-experimental.Dockerfile')
   dest=REPORT/'samba-dns-public-image.tar.gz';pipe=subprocess.Popen(['docker','-H','unix://'+SOCKET,'image','save',fixed],env=env,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
   with gzip.open(dest,'wb',compresslevel=1) as out:
    while True:
     data=pipe.stdout.read(1<<20)
     if not data:break
     out.write(data)
   assert pipe.wait(timeout=30)==0
   receipt['archive_sha256']=sha(dest);receipt['archive_bytes']=dest.stat().st_size;(REPORT/'samba-dns-overlay.json').write_text(json.dumps(receipt,indent=2))
   record('scoped_dns_candidate_image_and_consistent_e2e_revision',image_id=fixed_id,published=False)
  def apply():return cli('apply','-w',workspace,'-module-root',PRIVATE/'source/modules','-y','-no-snapshot','-update-lock','-json',timeout=1200)
  def compose_env(module):
   active=yaml.safe_load((workspace/'.anas/state/active.yml').read_text())['active_deployment'];folder=workspace/'.anas/deployments'/active/'modules'/module
   p=d('compose','-f',str(folder/'docker-compose.yml'),'--env-file',str(folder/'.env'),'config','--environment')
   return dict(x.split('=',1) for x in p.stdout.decode().splitlines() if '=' in x)
  def owned(suffix):
   x=json.loads(d('inspect',PREFIX+suffix).stdout)[0];assert x['Config']['Labels']['com.docker.compose.project.working_dir'].startswith(str(workspace/'.anas/deployments')+'/');return x
  def actions_ini():
   view=owned('forgejo');items=dict(x.split('=',1) for x in view['Config']['Env'] if '=' in x)
   allowed=['/var/lib/gitea/custom/conf/app.ini','/etc/gitea/app.ini','/var/lib/gitea/conf/app.ini'];preferred=items.get('GITEA_APP_INI')
   if preferred:assert preferred in allowed;allowed=[preferred]+[x for x in allowed if x!=preferred]
   for path in allowed:
    p=d('exec',PREFIX+'forgejo','cat',path,check=False)
    if p.returncode:continue
    parser=configparser.ConfigParser(interpolation=None,strict=False);parser.read_string('[DEFAULT]\n'+p.stdout.decode('utf-8-sig'));sections=[x for x in parser.sections() if x.lower()=='actions'];assert len(sections)==1
    value=parser.get(sections[0],'enabled').strip().lower();assert value in ['true','false'];return value=='true'
   raise RuntimeError('actual Forgejo Actions config unavailable')
  if args.resume_standard:
   stage='prepare_existing_repository_unit';old_env=compose_env('forgejo');old_lego=compose_env('lego')
   old_owner=old_env['FORGEJO_LOCAL_ADMIN__BREAK_GLASS_USERNAME'];old_store=yaml.safe_load((workspace/'.anas/secrets.yml').read_text());old_password=old_store['secrets']['ANAS_LOCAL_ADMIN__FORGEJO__BREAK_GLASS__PASSWORD']['value']
   class RecoveryNoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args,**kw):return None
   recovery=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(Path(old_lego['ANAS_TLS_CERTS_DIR'])/old_lego['ANAS_TLS_TRUST_BUNDLE_NAME']))),RecoveryNoRedirect())
   auth=base64.b64encode((old_owner+':'+old_password).encode()).decode();old_repo='/api/v1/repos/'+old_owner+'/core-actions'
   recovery_observations=[]
   def recovery_api(method,path,value=None):
    request=urllib.request.Request(BASE+path,method=method,data=None if value is None else json.dumps(value).encode(),headers={'Authorization':'Basic '+auth,'Content-Type':'application/json'})
    try:
     with recovery.open(request,timeout=10) as response:status=response.status;body=response.read(4<<20)
    except urllib.error.HTTPError as error:
     recovery_observations.append({'method':method,'kind':'version' if path=='/api/v1/version' else 'repository-api','status':error.code});(REPORT/'recovery-api-status.json').write_text(json.dumps(recovery_observations));raise
    recovery_observations.append({'method':method,'kind':'version' if path=='/api/v1/version' else 'repository-api','status':status});(REPORT/'recovery-api-status.json').write_text(json.dumps(recovery_observations))
    assert status in (200,201,204);return json.loads(body) if body else None
   deadline=time.monotonic()+300
   while True:
    try:recovery_api('GET','/api/v1/version');current=recovery_api('GET',old_repo);break
    except urllib.error.HTTPError as error:
     if error.code not in (404,502,503) or time.monotonic()>=deadline:raise
     time.sleep(2)
    except urllib.error.URLError as error:
     if not isinstance(error.reason,(ConnectionRefusedError,TimeoutError)) or time.monotonic()>=deadline:raise
     time.sleep(2)
   assert current['private'] is True and current['name']=='core-actions';before=current.get('has_actions')
   recovery_api('PATCH',old_repo,{'has_actions':True});assert recovery_api('GET',old_repo).get('has_actions') is True
   runners=recovery_api('GET',old_repo+'/actions/runners');assert isinstance(runners,(list,dict))
   active=yaml.safe_load((workspace/'.anas/state/active.yml').read_text())['active_deployment'];folder=workspace/'.anas/deployments'/active/'modules/forgejo'
   control=owned('forgejo_actions_controller');assert control['Path']=='/usr/local/bin/anas-forgejo-actions-controller'
   d('start',PREFIX+'forgejo_actions_controller',timeout=180)
   deadline=time.monotonic()+30
   while not owned('forgejo_actions_controller')['State']['Running']:
    assert time.monotonic()<deadline;time.sleep(.5)
   (REPORT/'repository-unit-recovery.json').write_text(json.dumps({'before':before,'after':True,'runner_api_available':True,'declared_controller_restarted':True,'state_files_modified':False}))
   record('existing_repository_unit_and_declared_controller_recovered_without_state_tampering')
  if args.resume_standard:
   stage='initial_disabled_configuration'
   cli('config','set','-w',workspace,'forgejo.actions_enabled','false','--json')
  if args.samba_full_image:
   assert args.resume_standard and re.fullmatch(r'sha256:[a-f0-9]{64}',args.samba_full_image)
   stage='prepare_full_samba_revision';module=PRIVATE/'source/modules/samba_dc';overlay=[]
   assert d('image','inspect','--format','{{.Id}}','ghcr.io/anas-project/anas-samba-dc:4.23.6-r14').stdout.decode().strip()==args.samba_full_image
   for name,key in [('module.yml','revision'),('localization.yml','module_revision')]:
    target=module/name;text,count=re.subn(r'(?m)^'+key+r': 14$',key+': 15',target.read_text());assert count==1;target.write_text(text);overlay.append({'path':str(target.relative_to(PRIVATE/'source')),'sha256':sha(target)})
   for target in module.rglob('*'):
    if target.is_file() and target.suffix in ('.yml','.md'):
     text=target.read_text()
     if '4.23.6-r14' in text:
      target.write_text(text.replace('4.23.6-r14','4.23.6-r15'));overlay.append({'path':str(target.relative_to(PRIVATE/'source')),'sha256':sha(target)})
   d('image','tag',args.samba_full_image,'ghcr.io/anas-project/anas-samba-dc:4.23.6-r15')
   d('image','tag','ghcr.io/anas-project/anas-samba-dc-anchor:4.23.6-r14','ghcr.io/anas-project/anas-samba-dc-anchor:4.23.6-r15')
   (REPORT/'full-samba-e2e-revision.json').write_text(json.dumps({'e2e_revision':15,'production_revision_changed':False,'full_samba_image':args.samba_full_image,'unchanged_anchor_retag_only':True,'source_overlays':overlay},indent=2))
   record('full_samba_image_selected_for_new_e2e_revision',image_id=args.samba_full_image,e2e_revision=15,production_revision_changed=False)
  stage='initial_disabled_apply'
  apply();e=compose_env('forgejo');lego=compose_env('lego')
  actual_images=[]
  for ref,name in [(CONTROLLER_REF,'forgejo_actions_controller'),('ghcr.io/anas-project/anas-forgejo:17.0.1-r1','forgejo')]:
   expected=next(x['id'] for x in public_images if x['ref']==ref);actual=owned(name)['Image'];actual_images.append({'ref':ref,'expected':expected,'actual':actual,'matches':actual==expected})
  (REPORT/'actual-runtime-images.json').write_text(json.dumps(actual_images))
  for binary,digest in [('anas-forgejo-actions-controller','25e6dfc109d114e456daa42ce8e6c1ea011363882fd9395162e3f3b57c69051e'),('incus','6f6c210f197b20bbb81faaaa55f3630da222f2b15654590056983d16585677ff')]:
   target=PRIVATE/('actual-'+binary);d('cp',PREFIX+'forgejo_actions_controller:/usr/local/bin/'+binary,str(target));assert sha(target)==digest
  record('actual_security_controller_and_incus_cli_elf_match')
  assert actions_ini() is False and e['FORGEJO_ACTIONS_ENABLED']=='false'
  control=owned('forgejo_actions_controller');assert control['State']['Status']=='exited' and control['State']['ExitCode']==0
  owner=e['FORGEJO_LOCAL_ADMIN__BREAK_GLASS_USERNAME'];store=yaml.safe_load((workspace/'.anas/secrets.yml').read_text());password=store['secrets']['ANAS_LOCAL_ADMIN__FORGEJO__BREAK_GLASS__PASSWORD']['value']
  ca=Path(lego['ANAS_TLS_CERTS_DIR'])/lego['ANAS_TLS_TRUST_BUNDLE_NAME']
  class NoRedirect(urllib.request.HTTPRedirectHandler):
   def redirect_request(self,*args,**kw):return None
  opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(ca))),NoRedirect())
  api_observations=[]
  def api(method,path,value=None,credentials=None,expected=(200,201,204)):
   user,pw=credentials or (owner,password);auth=base64.b64encode((user+':'+pw).encode()).decode()
   request=urllib.request.Request(BASE+path,method=method,data=None if value is None else json.dumps(value).encode(),headers={'Authorization':'Basic '+auth,'Content-Type':'application/json'})
   try:
    with opener.open(request,timeout=10) as response:body=response.read((4<<20)+1);code=response.status
   except urllib.error.HTTPError as error:body=error.read(65536);code=error.code
   kind='version' if path=='/api/v1/version' else 'create-repository' if path=='/api/v1/user/repos' else 'user' if path=='/api/v1/user' else 'repository-api'
   api_observations.append({'method':method,'kind':kind,'status':code});(REPORT/'api-status-observations.json').write_text(json.dumps(api_observations))
   assert code in expected and len(body)<=4<<20,'unexpected fixed fixture API status '+str(code)
   return json.loads(body) if body else None
  ready_deadline=time.monotonic()+120
  while True:
   try:version=api('GET','/api/v1/version')['version'];break
   except AssertionError:
    if not api_observations or api_observations[-1]['status'] not in (404,502,503) or time.monotonic()>=ready_deadline:raise
    time.sleep(2)
  assert version.split('+')[0]=='17.0.1'
  (REPORT/'actual-forgejo-version.json').write_text(json.dumps({'version':version}))
  repo='/api/v1/repos/'+owner+'/core-actions'
  if args.resume_standard:
   existing_repo=api('GET',repo);assert existing_repo['private'] is True and existing_repo['name']=='core-actions'
  else:api('POST','/api/v1/user/repos',{'name':'core-actions','private':True,'auto_init':True,'default_branch':'main'})
  record('initial_single_switch_disabled_and_cleanup_exited')
  helper_path=INPUTS/'server-forgejo-onejob-e2e.py';spec=importlib.util.spec_from_file_location('core_job_observation',helper_path);helper=importlib.util.module_from_spec(spec);spec.loader.exec_module(helper);helper.REPO=repo
  cli('config','set','-w',workspace,'forgejo.actions_allowed_scopes',owner+'/core-actions','--json')
  stage='enable_apply';cli('config','set','-w',workspace,'forgejo.actions_enabled','true','--json');apply();e=compose_env('forgejo')
  assert actions_ini() is True and e['FORGEJO_ACTIONS_ENABLED']=='true' and owned('forgejo_actions_controller')['State']['Running']
  managed_password=e['FORGEJO_ACTIONS_CONTROLLER_PASSWORD'];managed=api('GET','/api/v1/user',credentials=('anas_actions_controller',managed_password));managed_id=managed['id'];assert managed['login']=='anas_actions_controller'
  record('one_actions_switch_enables_server_and_controller',controller_id=managed_id)
  before=api('GET',repo).get('has_actions');api('PATCH',repo,{'has_actions':True});after=api('GET',repo).get('has_actions');assert after is True
  (REPORT/'repository-actions-unit.json').write_text(json.dumps({'before':before,'after':after,'setting':'upstream repository unit; no second Module Runner switch'}))
  revision=api('GET',repo+'/contents/.forgejo/workflows/core.yml')['sha'] if args.resume_standard else None
  def current_run(ident):
   rows=api('GET',repo+'/actions/runs');rows=rows['workflow_runs'] if isinstance(rows,dict) else rows;matches=[x for x in rows if x['id']==ident];assert len(matches)==1;return matches[0]
  def registrations():return helper.fixture_registrations(api,repo)
  def empty():
   counts=inventories();return counts=={'instances':0,'root_disks':0} and registrations()==[]
  def submit(case):
   nonlocal revision
   source=helper.workflow_source(case).replace('    runs-on: anas-native\n','    runs-on: docker\n    container:\n      image: public.ecr.aws/docker/library/busybox@sha256:7a3ebe5bfd1a4a19797d20b0c0bb39d44393e9a03fd852c0865b0f540d868df0\n')
   source+='\n# Core acceptance '+str(time.time_ns())+'\n'
   payload={'branch':'main','message':'ANAS Core '+case,'content':base64.b64encode(source.encode()).decode()}
   if revision:payload['sha']=revision
   response=api('PUT' if revision else 'POST',repo+'/contents/.forgejo/workflows/core.yml',payload);revision=response['content']['sha'];commit=response['commit']['sha'];deadline=time.monotonic()+60
   while time.monotonic()<deadline:
    rows=api('GET',repo+'/actions/runs');rows=rows['workflow_runs'] if isinstance(rows,dict) else rows;found=[x for x in rows if x.get('commit_sha')==commit]
    if len(found)==1:return found[0]['id']
    assert not len(found)>1;time.sleep(1)
   raise RuntimeError('fixed Core workflow not queued')
  stage='enabled_workflow';ident=submit('normal');deadline=time.monotonic()+300
  while time.monotonic()<deadline:
   result=helper.workflow_result(current_run(ident))
   if result is not None:assert result=='success'
   if result=='success' and empty():
    steps=[x for x in helper.fixture_job_steps(api,ident) if x['name']=='entered-test-payload'];assert len(steps)==1 and steps[0]['started']>0 and steps[0]['stopped']>0 and steps[0]['status']=='success';break
   time.sleep(2)
  else:raise RuntimeError('Core real workflow or reclamation timed out')
  record('ordinary_cli_default_network_real_workflow_and_reclamation')
  stage='running_before_disable';ident=submit('cancel');deadline=time.monotonic()+240
  while time.monotonic()<deadline:
   steps=[x for x in helper.fixture_job_steps(api,ident) if x['name']=='entered-test-payload']
   if len(steps)==1 and steps[0]['started']>0 and steps[0]['stopped']==0 and inventories()['instances']==1 and len(registrations())==1:break
   time.sleep(2)
  else:raise RuntimeError('Core interruption payload never started')
  frames=[];stop=threading.Event()
  def observe():
   while not stop.is_set():
    try:
     counts=inventories();runners=registrations();frames.append({'at':time.time(),'actions_api_confirmed':True,'registrations':len(runners),**counts})
    except Exception:frames.append({'at':time.time(),'actions_api_confirmed':False})
    stop.wait(.1)
  observer=threading.Thread(target=observe,daemon=True);observer.start();stage='disable_running_apply'
  try:cli('config','set','-w',workspace,'forgejo.actions_enabled','false','--json');apply()
  finally:stop.set();observer.join(timeout=20);(REPORT/'disable-observations.json').write_text(json.dumps(frames))
  assert any(x.get('actions_api_confirmed') and x['registrations']==0 and x['instances']==0 and x['root_disks']==0 for x in frames),'reclamation was not independently observed before disabling Actions API'
  e=compose_env('forgejo');assert actions_ini() is False and e['FORGEJO_ACTIONS_ENABLED']=='false' and inventories()=={'instances':0,'root_disks':0}
  control=owned('forgejo_actions_controller');assert control['State']['Status']=='exited' and control['State']['ExitCode']==0
  api('GET','/api/v1/user',credentials=('anas_actions_controller',managed_password),expected=(401,403))
  record('single_switch_disables_server_drains_running_guest_and_revokes_account',observations=len(frames))
  stage='reenable_apply';cli('config','set','-w',workspace,'forgejo.actions_enabled','true','--json');apply();e=compose_env('forgejo');assert actions_ini() is True and e['FORGEJO_ACTIONS_ENABLED']=='true'
  restored=api('GET','/api/v1/user',credentials=('anas_actions_controller',e['FORGEJO_ACTIONS_CONTROLLER_PASSWORD']));assert restored['id']==managed_id and empty()
  record('reenable_retains_managed_identity_and_zero_old_resources',controller_id=managed_id)
  stage='final_disable';cli('config','set','-w',workspace,'forgejo.actions_enabled','false','--json');apply();assert actions_ini() is False and inventories()=={'instances':0,'root_disks':0}
  stage='product_stop';cli('stop','-w',workspace,'-json',timeout=600)
  remaining=d('ps','-q').stdout.strip();assert not remaining
  record('ordinary_cli_stop_finishes_without_running_containers')
  summary={'passed':True,'requirement':'FORGEJO-R-052','source_identity':SOURCE_ID,'runner_fingerprint':FINGERPRINT,'isolation':'incus_container','database':'postgres','forgejo':'17.0.1','public_cli_executed':True,'default_network_policy_overrides':False,'checks':results}
  (REPORT/'summary.json').write_text(json.dumps(summary,indent=2));print(json.dumps(summary),flush=True)
 except Exception as error:
  if stage=='initial_disabled_apply' and docker is not None and docker.poll() is None:
   try:
    view=owned('traefik');forge=owned('forgejo')
    probe={'endpoint_matches_fixture':compose_env('forgejo').get('FORGEJO_DOMAIN_FULL')==BASE,'socket_mounts':[{'source':x['Source'],'destination':x['Destination']} for x in view.get('Mounts',[]) if x['Destination']=='/var/run/docker.sock'],'traefik_networks':sorted(view['NetworkSettings']['Networks']),'forgejo_networks':sorted(forge['NetworkSettings']['Networks']),'routing_labels':{k:v for k,v in forge['Config']['Labels'].items() if k in ['traefik.enable','anas.traefik.instance','traefik.http.routers.forgejo.entrypoints','traefik.http.routers.forgejo.rule','traefik.http.services.forgejo.loadbalancer.server.port']}}
    messages=d('logs','--tail','200',PREFIX+'traefik',check=False);body=(messages.stdout+messages.stderr).decode(errors='replace')
    probe['known_log_classes']=[x for x in ['client version','minimum supported API version','permission denied','Cannot connect','Unable to create client','Failed to retrieve information','Error while building configuration','failed to decode','unsupported API version','does not exist','connection refused'] if x in body]
    (REPORT/'bounded-routing-diagnostic.json').write_text(json.dumps(probe))
   except Exception:pass
  (REPORT/'summary.json').write_text(json.dumps({'passed':False,'requirement':'FORGEJO-R-052','stage':stage,'error_type':type(error).__name__,'error_line':next((x.lineno for x in reversed(traceback.extract_tb(error.__traceback__)) if x.filename==__file__),0),'private_command_count':counter,'known_failure_classes':[marker for marker in ['No such image','failed to solve','failed to resolve source metadata','TLS handshake timeout','i/o timeout','no such host','module lookup disabled by GOPROXY=off','no space left on device','failed to authorize'] if any(marker in p.read_text(errors='replace') for p in sorted(logs.glob('*.stderr'))[-1:])],'checks':results},indent=2));raise
 finally:
  if args.standard_host:
   public_host_reports=Path('/opt/anas-host-action-e2e/reports')
   if public_host_reports.is_dir():
    target=REPORT/'host-public-jobs';target.mkdir(mode=0o700,exist_ok=True)
    for p in public_host_reports.glob('job-*.json'):shutil.copyfile(p,target/p.name)
   run(['systemctl','stop','anasd.service','anas-hostd.socket'],timeout=30,check=False)
  if docker is not None and docker.poll() is None:
   docker.terminate()
   try:docker.wait(timeout=60)
   except subprocess.TimeoutExpired:docker.kill();docker.wait(timeout=15)
  (REPORT/'owned-docker-exit.json').write_text(json.dumps({'private_docker_exited':docker is None or docker.poll() is not None}))

if __name__=='__main__':
 parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--vm-id',required=True);parser.add_argument('--resume-prepared',action='store_true');parser.add_argument('--dns-fix',action='store_true');parser.add_argument('--standard-host',action='store_true');parser.add_argument('--resume-standard',action='store_true');parser.add_argument('--samba-full-image')
 args=parser.parse_args()
 if args.standard_host:POOL='anas-btrfs'
 if args.resume_prepared:REPORT=Path('/opt/anas-lifecycle-report-core-continued')
 if args.resume_standard:REPORT=Path('/opt/anas-lifecycle-report-core-final')
 try:main(args)
 except Exception as error:
  print(json.dumps({'passed':False,'error_type':type(error).__name__,'detail':'see private command logs and bounded public stage summary'}),flush=True);raise SystemExit(1)
