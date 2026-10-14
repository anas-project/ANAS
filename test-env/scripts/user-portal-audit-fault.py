#!/usr/bin/env python3
# TEST_CASES: PORTAL-T-003
"""Inotify fault injection after a durable intent, scoped to a private fixture."""
import ctypes
import json
import os
from pathlib import Path
import re
import select
import sys

root=Path(sys.argv[1])
if not re.fullmatch(r'/home/whl/anas-user-portal-e2e-test-[A-Za-z0-9-]+',str(root)):
    raise SystemExit('isolated fixture required')
audit=root/'workspace/data/user_portal/audit/audit.jsonl'
assert audit.resolve()==audit and not audit.is_symlink()
initial=audit.stat().st_size
libc=ctypes.CDLL(None,use_errno=True)
fd=libc.inotify_init1(os.O_CLOEXEC|os.O_NONBLOCK)
assert fd>=0
assert libc.inotify_add_watch(fd,os.fsencode(audit),2)>=0
(root/'private/audit-fault-ready').write_text('ready')
while True:
    if not select.select([fd],[],[],45)[0]:
        raise SystemExit('intent did not arrive')
    os.read(fd,65536)
    data=audit.read_bytes()
    if len(data)<=initial or not data.endswith(b'\n'):
        continue
    record=json.loads(data.splitlines()[-1])
    if record['phase']!='intent':
        initial=len(data)
        continue
    # Preserve the original durable inode. Replacing its path makes the live
    # result append fail identity verification without touching directory data.
    saved=audit.with_name('audit-held.jsonl')
    assert not saved.exists()
    audit.rename(saved)
    audit.write_bytes(b'')
    os.chown(audit,65532,65532)
    audit.chmod(0o600)
    (root/'evidence/audit-fault-injected.json').write_text(json.dumps({'intent_id':record['id'],'after_intent':True,'original_preserved':True}))
    break
os.close(fd)
