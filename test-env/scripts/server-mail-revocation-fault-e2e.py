from pathlib import Path
import hashlib,json,re,subprocess,time,sys,signal
# TEST_CASES: MAILMOD-T-004
assert len(sys.argv)==4
fixture_run,run_id,provider=sys.argv[1:]
assert all(re.fullmatch('[0-9]{1,6}',v) for v in [fixture_run,run_id]) and provider in ['casdoor','llng']
root=Path('/home/whl/anas-mail-runtime-20261026');scope=root/'formal-runtime-26';prefix='anas_mail_formal26_'+provider+'_';wrapper=root/'docker-test'
evidence=scope/('evidence-'+provider+'-r034-'+run_id);assert not evidence.exists();evidence.mkdir(mode=0o700)
abort=scope/('r034-'+run_id+'-abort');assert not abort.exists()
control=scope/('r034-'+run_id+'-revoked');proof=scope/('r034-'+run_id+'-browser.json');assert not control.exists() and proof.exists() and proof.stat().st_mode&0o777==0o600
recovering=False
def request_abort(signum,frame):
 if not recovering:raise RuntimeError('fault controller interrupted')
for signum in [signal.SIGTERM,signal.SIGHUP]:signal.signal(signum,request_abort)
def emit(value):
 try:print(json.dumps(value),flush=True)
 except BrokenPipeError:pass

failure=None;paused=False;ad_stopped=False;result={}
def run(args,name=None,timeout=180,check=True,data=None):
 p=subprocess.run([str(x) for x in args],input=data,capture_output=True,timeout=timeout)
 if name:(evidence/name).write_bytes(p.stdout+p.stderr)
 if check and p.returncode:raise RuntimeError('test operation failed: '+str(name or args[0]))
 return p
def d(*args,**kw):return run([wrapper,*args],**kw)
def business():return sorted(run(['sudo','-n','docker','ps','-a','--format','{{.ID}} {{.Names}}']).stdout.decode().splitlines())
def state(suffix):return json.loads(d('inspect',prefix+suffix,'--format','{{json .State}}').stdout)
def process_privileges(current):
 cid=d('inspect',prefix+'mail','--format','{{.Id}}').stdout.decode().strip();assert re.fullmatch('[a-f0-9]{64}',cid)
 code="import json,sys,re\nfrom pathlib import Path\npid,cid=json.loads(sys.argv[1]);assert isinstance(pid,int) and pid>1 and re.fullmatch('[a-f0-9]{64}',cid)\np=Path('/proc')/str(pid);assert cid in (p/'cgroup').read_text()\nfields={line.partition(':')[0]:line.partition(':')[2].split() for line in (p/'status').read_text().splitlines() if line.startswith(('Uid:','CapEff:'))}\nprint(json.dumps(fields))\n"
 return json.loads(run(['sudo','-n','python3','-c',code,json.dumps([current['Pid'],cid])]).stdout)
def healthy(suffix):
 deadline=time.monotonic()+180
 while time.monotonic()<deadline:
  current=state(suffix)
  if current.get('Health',{}).get('Status')=='healthy':return
  if not current.get('Running'):raise RuntimeError('test service stopped: '+suffix)
  time.sleep(1)
 raise RuntimeError('test service health timeout: '+suffix)
before=business()
try:
 assert d('info','--format','{{.DockerRootDir}}').stdout.decode().strip()=='/home/whl/anas-mail-go-test-20261024/docker'
 assert (scope/('browser-'+fixture_run+'-input.json')).exists() and not (scope/('browser-'+fixture_run+'.done')).exists()
 value=json.loads(proof.read_text());assert set(value)=={'token_hash'} and re.fullmatch('[a-f0-9]{64}',value['token_hash'])
 env=dict(x.split('=',1) for x in json.loads(d('inspect',prefix+'mail','--format','{{json .Config.Env}}').stdout) if '=' in x)
 user,database,password=env['MAIL_DB_USERNAME'],env['MAIL_DB_NAME'],env['MAIL_DB_PASSWORD'];assert re.fullmatch('[a-zA-Z_0-9]+',user) and re.fullmatch('[a-zA-Z_0-9]+',database)
 def sql(statement):
  p=d('exec','-i',prefix+'postgres','sh','-c','IFS= read -r PGPASSWORD; export PGPASSWORD; exec psql -X -h 127.0.0.1 -U "$1" -d "$2" -v ON_ERROR_STOP=1 -At -c "$3"','mail-fault-db',user,database,statement,data=(password+'\n').encode(),timeout=12)
  return p.stdout.decode().strip()
 sid=sql("SELECT id::text FROM mail.web_session WHERE token_hash=decode('"+value['token_hash']+"','hex') AND revoked_at IS NULL AND expires_at>clock_timestamp();");assert re.fullmatch('[a-f0-9-]{36}',sid),'actual browser session not found'
 people=sql("SELECT count(*) FROM mail.person WHERE directory_state='enabled';")
 assert state('mail_dms').get('Health',{}).get('Status')=='healthy' and state('samba_dc')['Running']
 initial=process_privileges(state('mail'));assert initial['Uid']==['65532']*4 and initial['CapEff']==['0000000000000000'],'initial Go privilege witness failed'
 d('pause',prefix+'mail_dms');paused=True
 emit({'stage':'DMS_paused_browser_may_revoke','scope':'isolated-'+provider,'actual_browser_session_found':True})
 deadline=time.monotonic()+180
 while not control.exists() and not abort.exists() and time.monotonic()<deadline:time.sleep(1)
 assert control.exists(),'browser revoke signal timed out'
 task=sql("SELECT id FROM mail.revocation_task WHERE session_id='"+sid+"' AND mailbox_id IS NULL AND completed_at IS NULL;");assert task.isdigit(),'durable uncompleted withdrawal missing';tid=int(task)
 for _ in range(25):
  attempts=int(sql(f'SELECT attempts FROM mail.revocation_task WHERE id={tid};'))
  if attempts>=1:break
  time.sleep(1)
 else:raise RuntimeError('withdrawal was not attempted while target paused')
 assert sql(f'SELECT completed_at IS NULL FROM mail.revocation_task WHERE id={tid};')=='t','unavailable target falsely completed'
 result['target_unavailable_task_retained']=True
 d('stop','--time','15',prefix+'samba_dc',timeout=35);ad_stopped=True
 assert not state('samba_dc')['Running']
 emit({'stage':'AD_stopped_pending_task_confirmed'})
 d('restart','--time','15',prefix+'mail',timeout=40)
 deadline=time.monotonic()+40
 while time.monotonic()<deadline:
  current=state('mail')
  if current.get('Running') and not current.get('Restarting') and current.get('Pid',0)>1:
   process=process_privileges(current);uid=process['Uid']
   if uid==['65532']*4:break
  time.sleep(1)
 else:raise RuntimeError('Go worker did not remain running without AD')
 caps=process['CapEff'];assert caps==['0000000000000000']
 started_at=current['StartedAt']
 result['Go_restart_with_AD_stopped']=True
 result['authorization_health_during_fault']=d('exec',prefix+'mail','/anas-mail','healthcheck',check=False).returncode==0
 emit({'stage':'Go_restarted_unprivileged_without_AD','authorization_health':result['authorization_health_during_fault']})
 assert sql(f'SELECT completed_at IS NULL FROM mail.revocation_task WHERE id={tid};')=='t'
 d('unpause',prefix+'mail_dms');paused=False
 emit({'stage':'DMS_unpaused_AD_still_stopped'})
 deadline=time.monotonic()+120
 while time.monotonic()<deadline:
  assert not abort.exists(),'controller aborted fault test'
  done=sql(f'SELECT completed_at IS NOT NULL FROM mail.revocation_task WHERE id={tid};')
  if done=='t':break
  time.sleep(1)
 else:raise RuntimeError('withdrawal did not complete while AD stopped')
 assert not state('samba_dc')['Running'],'AD unexpectedly recovered before withdrawal'
 assert state('mail')['StartedAt']==started_at and not state('mail')['Restarting'],'Go restarted while task was pending'
 emit({'stage':'withdrawal_completed_without_AD'})
 assert sql("SELECT count(*) FROM mail.person WHERE directory_state='enabled';")==people,'LDAP failure cleared enabled people'
 result.update({'durable_withdrawal_completed_while_AD_stopped':True,'directory_failure_did_not_clear_people':True,'attempts':int(sql(f'SELECT attempts FROM mail.revocation_task WHERE id={tid};'))})
except BaseException as exc:failure=type(exc).__name__+': '+str(exc)
finally:
 recovering=True
 cleanup=[]
 if paused:
  try:d('unpause',prefix+'mail_dms');paused=False
  except BaseException as exc:cleanup.append(str(exc))
 if ad_stopped:
  try:
   emit({'stage':'restoring_AD'})
   d('start',prefix+'samba_dc');healthy('samba_dc');ad_stopped=False
  except BaseException as exc:cleanup.append(str(exc))
 for suffix in ['mail','mail_dms','samba_dc']:
  try:healthy(suffix)
  except BaseException as exc:cleanup.append(str(exc))
 proof.unlink(missing_ok=True)
 for suffix in ['mail','mail_dms','samba_dc']:
  run(['sudo','-n','sh','-c','umask 077; "$1" logs "$2" > "$3" 2>&1','fault-log',wrapper,prefix+suffix,evidence/(suffix+'.log')],check=False)
 result.update({'failure':failure,'cleanup_errors':cleanup,'business_unchanged':business()==before,'business_count':len(before)})
 (evidence/'result.json').write_text(json.dumps(result,indent=2));print(json.dumps(result),flush=True)
if failure or cleanup or not result['business_unchanged']:raise SystemExit(1)
