# TEST_CASES: MAILMOD-T-009
"""Read only bounded, non-secret metadata from a private test message.

The From header is untrusted and is not proof of sender authentication. Never
print message bodies, addresses, URLs, query strings, or one-time credentials.
"""
import email, email.policy, email.utils, json, os, pathlib, re, sys
from html.parser import HTMLParser
from urllib.parse import urlsplit

if len(sys.argv) != 2:
    raise SystemExit('private test message required')
p = pathlib.Path(sys.argv[1])
st = p.lstat()
if p.is_symlink() or not p.is_file() or st.st_uid != os.getuid() or st.st_mode & 0o077 or st.st_size > 16 * 1024 * 1024:
    raise SystemExit('private bounded message required')
message = email.message_from_bytes(p.read_bytes(), policy=email.policy.default)
addresses = email.utils.getaddresses(message.get_all('From', []))
domains = sorted({address.rpartition('@')[2].lower() for _, address in addresses if re.fullmatch(r'[a-zA-Z0-9.-]{1,253}', address.rpartition('@')[2])})
hosts = set()
def add_url(value):
    try:
        u = urlsplit(value)
        if u.scheme == 'https' and u.hostname and re.fullmatch(r'[a-zA-Z0-9.-]{1,253}', u.hostname): hosts.add(u.hostname.lower())
    except ValueError: pass
class Links(HTMLParser):
    def handle_starttag(self, tag, attrs):
        if tag == 'a':
            for key, value in attrs:
                if key == 'href' and value: add_url(value)
for part in message.walk():
    if part.get_content_type() not in ('text/html','text/plain') or part.get_content_disposition() == 'attachment': continue
    try: content=part.get_content()
    except (LookupError, UnicodeError, ValueError): continue
    if not isinstance(content,str) or len(content)>2*1024*1024: continue
    if part.get_content_type() == 'text/html':
        parser=Links();parser.feed(content)
    for match in re.findall(r'https://[^\s<>"\']+',content): add_url(match)
print(json.dumps({'from_domains':domains,'https_link_hosts':sorted(hosts),'from_header_is_authenticated':False}))
