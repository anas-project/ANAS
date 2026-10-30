from pathlib import Path
import json,os,re,selectors,subprocess,time,sys
# TEST_CASES: MAILMOD-T-004
assert len(sys.argv)==3
fixture_run,run_id=sys.argv[1:];assert all(re.fullmatch('[0-9]{1,6}',v) for v in [fixture_run,run_id])
repo=Path(__file__).resolve().parents[2];file=Path(os.environ['MAIL_FORMAL_BROWSER_INPUT']);out=Path(os.environ['MAIL_FORMAL_BROWSER_OUTPUT'])
assert file.is_absolute() and out.is_absolute() and file.is_file() and not file.is_symlink() and file.stat().st_mode&0o777==0o600 and file.parent.stat().st_mode&0o777==0o700 and file.stat().st_size<=1024*1024
provider=json.loads(file.read_text())['provider'];assert provider in ['casdoor','llng']
out.mkdir(mode=0o700);remote='/home/whl/anas-mail-runtime-20261026';scope=remote+'/formal-runtime-26';host='whl@finance.hlong.wang'
assert not (out/'result.json').exists()

def command(args,timeout=40,check=True):
 p=subprocess.run(args,capture_output=True,timeout=timeout)
 if check and p.returncode:raise RuntimeError('coordination command failed: '+args[0])
 return p
def ssh(code,**kw):return command(['ssh',host,code],**kw)
def stage(value):print(json.dumps(value),flush=True)
def private(path,text):
 part=path.with_name(path.name+'.partial')
 with open(part,'x',opener=lambda p,f:os.open(p,f,0o600)) as f:f.write(text)
 assert not path.exists();os.replace(part,path)
browser=None;server=None;failure=None;remote_finished=False;sent=False;server_log=None;browser_log=None
try:
 command(['scp',str(repo/'test-env/scripts/server-mail-revocation-fault-e2e.py'),host+':'+remote+'/'])
 env=dict(os.environ,MAIL_FORMAL_BROWSER_INPUT=str(file),MAIL_FORMAL_BROWSER_OUTPUT=str(out),MAIL_FORMAL_CHROME_EXECUTABLE='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
 browser_log=open(out/'browser.log','x');os.chmod(out/'browser.log',0o600)
 browser=subprocess.Popen(['node',str(repo/'test-env/playwright/mail-formal-sso.mjs'),'--revocation-fault'],env=env,cwd=repo,stdout=browser_log,stderr=subprocess.STDOUT)
 deadline=time.monotonic()+180
 while not (out/'r034-browser.json').exists():
  if browser.poll() is not None:raise RuntimeError('browser failed before fault session was ready')
  if time.monotonic()>deadline:raise RuntimeError('browser bootstrap deadline exceeded')
  time.sleep(.25)
 stage({'stage':'actual_ordinary_browser_session_ready'})
 command(['scp',str(out/'r034-browser.json'),host+':'+scope+'/r034-'+run_id+'-browser.json'])
 server_log=open(out/'server.log','x');os.chmod(out/'server.log',0o600)
 server=subprocess.Popen(['ssh',host,'python3 '+remote+'/server-mail-revocation-fault-e2e.py '+fixture_run+' '+run_id+' '+provider],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,bufsize=1)
 sel=selectors.DefaultSelector();sel.register(server.stdout,selectors.EVENT_READ);deadline=time.monotonic()+900
 def line_received(line):
  server_log.write(line);server_log.flush()
  try:value=json.loads(line)
  except ValueError:return
  if 'stage' in value:
   stage(value)
   if value['stage']=='DMS_paused_browser_may_revoke':private(out/'dms-paused','automatic verified pause\n')
 while server.poll() is None:
  if time.monotonic()>deadline:raise RuntimeError('remote fault deadline exceeded')
  if browser.poll() is not None and not (out/'browser-revoked').exists():raise RuntimeError('browser exited before persisting revocation')
  for key,mask in sel.select(.2):
   line=key.fileobj.readline()
   if line:line_received(line)
  if not sent and (out/'browser-revoked').exists():
   value=json.loads((out/'browser-revoked').read_text());assert value=={'status':200}
   ssh('touch '+scope+'/r034-'+run_id+'-revoked');sent=True;stage({'stage':'browser_revocation_automatically_signalled'})
 for line in server.stdout:line_received(line)
 remote_finished=True
 # The browser waits for this path. scp creates it before writing its content;
 # publish only a complete, validated result so readers never parse partial JSON.
 partial=out/'fault-result.partial.json'
 command(['scp',host+':'+scope+'/evidence-'+provider+'-r034-'+run_id+'/result.json',str(partial)])
 os.chmod(partial,0o600);json.loads(partial.read_text())
 os.replace(partial,out/'fault-result.json')
 browser.wait(timeout=100)
 fault=json.loads((out/'fault-result.json').read_text());result=json.loads((out/'result.json').read_text())
 if server.returncode or browser.returncode or fault.get('failure') or not result.get('success'):raise RuntimeError('fault or browser acceptance failed; inspect private results')
 stage({'success':True,'server_withdrawal_without_AD':fault.get('durable_withdrawal_completed_while_AD_stopped'),'browser_old_cookies_denied':True,'business_unchanged':fault.get('business_unchanged')})
except BaseException as exc:failure=type(exc).__name__+': '+str(exc)
finally:
 if server is not None and server.poll() is None:
  ssh('touch '+scope+'/r034-'+run_id+'-abort',check=False)
  try:server.wait(timeout=360);remote_finished=True
  except subprocess.TimeoutExpired:failure=(failure or '')+'; remote recovery not confirmed'
 if browser is not None and browser.poll() is None:
  browser.terminate()
  try:browser.wait(timeout=10)
  except subprocess.TimeoutExpired:browser.kill();browser.wait()
 if server_log:server_log.close()
 if browser_log:browser_log.close()
 summary={'failure':failure,'remote_finished':remote_finished,'revocation_signal_sent':sent}
 (out/'coordinator-result.json').write_text(json.dumps(summary,indent=2));stage(summary)
if failure:raise SystemExit(1)
