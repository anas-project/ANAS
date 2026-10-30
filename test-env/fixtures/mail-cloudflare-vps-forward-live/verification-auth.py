# TEST_CASES: MAILMOD-T-009
"""Opt-in isolated verification-message DKIM check; never prints tokens or mail."""
from email import policy,utils
from email.parser import BytesParser
import json, os, pathlib, re, shlex, sys, urllib.parse, urllib.request
import dkim  # Test-only dependency, supplied externally; not a product dependency.
if len(sys.argv)!=3: raise SystemExit('private test email and destination intent required')
paths=[pathlib.Path(x) for x in sys.argv[1:]]
for p in paths:
    st=p.lstat()
    if p.is_symlink() or not p.is_file() or st.st_uid!=os.getuid() or st.st_mode&0o077 or st.st_size>16*1024*1024: raise SystemExit('private test inputs required')
data=paths[0].read_bytes(); intent=json.loads(paths[1].read_text());target=intent['target']
if not re.fullmatch(r'anas-[a-f0-9]{32}@vps-target\.mail-poc\.bengbu\.cc\.cd',target):raise SystemExit('isolated destination required')
m=BytesParser(policy=policy.default).parsebytes(data,headersonly=True)
if any(len(m.get_all(name,[]))!=1 for name in ('From','To','Subject')):raise SystemExit('ambiguous signed headers')
to=[v.lower() for _,v in utils.getaddresses(m.get_all('To',[]))]
from_addr=utils.parseaddr(str(m.get('From','')))[1].lower()
headers=m.get_all('DKIM-Signature',[]);names=[]
for h in headers:
    raw=str(h);d=re.search(r'(?:^|;)\s*d=([A-Za-z0-9.-]+)',raw);s=re.search(r'(?:^|;)\s*s=([A-Za-z0-9._-]+)',raw);fields=re.search(r'(?:^|;)\s*h=([^;]+)',raw)
    if not d or not s or not fields:raise SystemExit('signature fields unrecognized')
    domain=d.group(1).lower();selector=s.group(1).lower();signed=[v.strip().lower() for v in fields.group(1).split(':')]
    if domain not in ('notify.cloudflare.com','cloudflare-smtp.com') or not re.fullmatch(r'[a-z0-9_-]{1,63}',selector) or not {'from','to','subject'} <= set(signed):raise SystemExit('signature binding unrecognized')
    names.append((domain,selector+'._domainkey.'+domain+'.'))
allowed={name for _,name in names}
def dnsfunc(name,timeout=5):
    name=name.decode('ascii').lower()
    if name not in allowed:raise RuntimeError('unexpected DNS query')
    u='https://cloudflare-dns.com/dns-query?'+urllib.parse.urlencode({'name':name,'type':'TXT'})
    with urllib.request.urlopen(urllib.request.Request(u,headers={'Accept':'application/dns-json'}),timeout=timeout) as response:result=json.load(response)
    values=[a.get('data','') for a in result.get('Answer',[]) if a.get('type')==16]
    if result.get('Status')!=0 or len(values)!=1:return None
    return ''.join(shlex.split(values[0])).encode('ascii')
verifier=dkim.DKIM(data);results=[]
for i,(domain,_) in enumerate(names):
    try:valid=verifier.verify(idx=i,dnsfunc=dnsfunc)
    except Exception:valid=False
    results.append({'domain':domain,'valid':valid})
summary={'signatures':results,'dns_source':'Cloudflare DNS over HTTPS; DNSSEC not separately validated', 'test_destination_matches':to==[target.lower()], 'from_domain_matches':from_addr.endswith('@notify.cloudflare.com')}
print(json.dumps(summary))
if not results or not any(x['domain']=='notify.cloudflare.com' and x['valid'] for x in results) or not summary['test_destination_matches'] or not summary['from_domain_matches']:raise SystemExit('verification identity not established')
