# TEST_CASES: MAILPOC-T-004, MAILPOC-T-005, MAILPOC-T-006
"""Directory experiment configuration; all paths stay inside the new fixture."""
import json
import shutil

BACKEND = r'''
class mailgo_directory_backend extends rcube_addressbook
{
    public $primary_key='ID';
    public $readonly=true;
    public $groups=true;
    private $plugin;
    private $filter=[];
    private $result;
    public function __construct($plugin) { $this->plugin=$plugin; $this->ready=true; }
    public function get_name() { return 'Company directory'; }
    public function set_search_set($filter): void { $this->filter=$filter ?: []; }
    public function get_search_set() { return $this->filter; }
    public function reset(): void { $this->filter=[]; $this->result=null; }
    private function query($extra=[]) {
        return $this->plugin->api('addressbooks/query',array_merge(['book'=>'company','session_ref'=>$_SESSION['mailgo']['session_ref'],'group'=>($this->group_id ?: '')],$this->filter,$extra));
    }
    public function list_records($cols=null,$subset=0,$nocount=false) {
        $data=$this->query();$contacts=$data['contacts'];
        $this->result=new rcube_result_set(count($contacts),($this->list_page-1)*$this->page_size);
        foreach (array_slice($contacts,($this->list_page-1)*$this->page_size,$this->page_size) as $c) $this->result->add($c);
        return $this->result;
    }
    public function count() { return new rcube_result_set(count($this->query()['contacts'])); }
    public function get_result() { return $this->result; }
    public function search($fields,$value,$mode=0,$select=true,$nocount=false,$required=[]) {
        $this->filter=[($fields==='ID'?'id':'q')=>is_array($value)?implode(' ',$value):(string)$value];
        return $this->list_records();
    }
    public function get_record($id,$assoc=false) {
        $data=$this->query(['id'=>$id]);
        if ($assoc) return $data['contacts'][0]??null;
        $r=new rcube_result_set(count($data['contacts']));foreach($data['contacts'] as $c)$r->add($c);return $r;
    }
    public function list_groups($search=null,$mode=0) {
        return array_values(array_filter($this->query()['groups'],fn($g)=>!$search || stripos($g['name'],$search)!==false));
    }
    public function get_group($id) { foreach($this->list_groups() as $g)if($g['ID']===$id)return $g;return null; }
    public function get_record_groups($id) {
        $c=$this->get_record($id,true);$out=[];foreach($this->list_groups() as $g)if(in_array($g['ID'],$c['groups']??[]))$out[$g['ID']]=$g['name'];return $out;
    }
    public function set_group($id) { $this->group_id=$id; }
}
'''

AUDIT_PUMP = '''#!/usr/bin/env python3
import sys,time
from pathlib import Path
sys.path.insert(0,'/fixture/anchor')
from anchor_worker import AuditFollower
from event_journal import EventJournal,parse_audit_line,normalize_change,DEFAULT_ATTRIBUTES
path=Path('/var/log/samba-audit/dsdb.json');path.parent.mkdir(parents=True,exist_ok=True);path.touch(exist_ok=True)
f=AuditFollower(path);f.open_at_end();journal=EventJournal(Path('/events/events.jsonl'))
attrs=DEFAULT_ATTRIBUTES+('proxyAddresses','givenName','sn','department','telephoneNumber','title','mobile')
while True:
    for line in f.lines():
        record=parse_audit_line(line)
        if not record: continue
        event=normalize_change(record,attrs,lambda dn:dn.lower().endswith(',cn=users,dc=mailtest,dc=invalid'))
        if event: journal.append(event)
    time.sleep(.2)
'''

KICK = '''#!/usr/bin/env python3
import base64,hmac,json,re,subprocess
from http.server import BaseHTTPRequestHandler,HTTPServer
key=open('/tmp/docker-mailserver/kick-key').read().strip()
expected='Basic '+base64.b64encode(('kick:'+key).encode()).decode()
class Handler(BaseHTTPRequestHandler):
    def log_message(self,*args): pass
    def do_POST(self):
        try:
            assert self.path=='/kick' and hmac.compare_digest(self.headers.get('Authorization',''),expected)
            length=int(self.headers.get('Content-Length','0'));assert 0<length<256
            sid=json.loads(self.rfile.read(length))['sid'];assert re.fullmatch('[a-f0-9]{64}',sid)
            p=subprocess.run(['doveadm','kick','-f','user_mailgo_sid',sid],capture_output=True,timeout=3)
            status=204 if p.returncode==0 else 503
        except Exception: status=403
        self.send_response(status);self.end_headers()
HTTPServer(('0.0.0.0',8008),Handler).serve_forever()
'''

def extend(root, source, cfg, secret, write):
    cfg['services']['iam']['image']='sha256:0e50fcf6503a38858aca6a2e52b29f481b3a4cf3306747c1e74c65738d4ce1aa'
    cfg['services']['postgres']['image']='sha256:339c4dd98a38d35ef9ad4c9164231c9905c4cbd046287813fb8170f5f3ddd362'
    write('fixture/kick.py',KICK,0o644)
    write('data/dms/config/kick-key',secret['kick'],0o600)
    write('data/dms/config/user-patches.sh', '''#!/bin/bash
set -e
cat >/etc/supervisor/conf.d/mailgo-kick.conf <<'CONF'
[program:mailgo-kick]
command=/usr/bin/python3 /fixture/kick.py
autostart=true
autorestart=true
stdout_logfile=/var/log/mail/kick-adapter.log
stderr_logfile=/var/log/mail/kick-adapter-error.log
CONF
supervisorctl reread
supervisorctl update
''',0o755)
    cfg['services']['mail']['volumes']+=['./fixture/kick.py:/fixture/kick.py:ro']
    write('fixture/audit-pump.py',AUDIT_PUMP,0o644)
    # Preserve exact current producer implementation; augment only this fixture's
    # watched attribute configuration. No production module edits are performed.
    anchor=source/'anchor-source'
    if not anchor.exists(): anchor=source.parents[1]/'modules/samba_dc/anchor_worker'
    for name in ['anchor_worker.py','event_journal.py','mail_policy.py']:
        write('fixture/anchor/'+name,(anchor/name).read_text(),0o644)
    for name in ['events','audit']:(root/'data'/name).mkdir(mode=0o755,exist_ok=True)
    (root/'data/events/events.jsonl').touch(mode=0o644)
    cfg['services']['directory']['volumes']+=['./data/events:/events','./data/audit:/var/log/samba-audit']
    cfg['services']['manager']['volumes']+=['./data/events:/events:ro']
    start=(root/'fixture/samba-start.sh').read_text().replace('  tls enabled = yes','  log level = 1 dsdb_json_audit:5@/var/log/samba-audit/dsdb.json\n  tls enabled = yes')
    start=start.replace('exec samba -i --no-process-group','python3 /fixture/audit-pump.py >/var/log/samba-audit/pump.log 2>&1 &\nexec samba -i --no-process-group')
    # Interactive mode forces audit output to stdout; the journal tails a file.
    start=start.replace('exec samba -i --no-process-group','exec samba -F --no-process-group')
    write('fixture/samba-start.sh',start,0o755)
    users=(root/'fixture/samba-users.py').read_text()+'''
for name in ('MailUsers','Finance','Nested'):
    db.newgroup(name)
    row=db.search(base='DC=mailtest,DC=invalid',expression='(&(objectClass=group)(cn='+name+'))',attrs=['objectGUID'])[0]
    msg=ldb.Message();msg.dn=row.dn;msg['anasIdentityAnchor']=ldb.MessageElement(str(uuid.UUID(bytes_le=bytes(row['objectGUID'][0]))),ldb.FLAG_MOD_REPLACE,'anasIdentityAnchor');db.modify(msg)
db.add_remove_group_members('MailUsers',['alice','bob','Nested'],add_members_operation=True)
db.add_remove_group_members('Finance',['alice'],add_members_operation=True)
'''
    write('fixture/samba-users.py',users,0o644)
    llng=json.loads((root/'data/llng/custom.json').read_text())
    llng['oidcRPMetaDataOptions']['mailgo'].update(oidcRPMetaDataOptionsLogoutUrl='https://manage.mailtest.invalid:18884/backchannel',oidcRPMetaDataOptionsLogoutType='back',oidcRPMetaDataOptionsLogoutSessionRequired=1)
    write('data/llng/custom.json',json.dumps(llng))
