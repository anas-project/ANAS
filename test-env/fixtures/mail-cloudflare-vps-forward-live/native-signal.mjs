// TEST_CASES: MAILMOD-T-009 MAILMOD-T-080
// The signal contains opaque source IDs only, never verification content.
const messageID = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;

export function nativeReadyPayload(verification, message) {
  if (typeof verification !== 'string' || typeof message !== 'string' || !messageID.test(verification) || !messageID.test(message) || verification === message) {
    throw Error('invalid isolated message identifiers');
  }
  return Buffer.from(JSON.stringify({VerificationID: verification, MessageID: message}));
}

export function nativeReadyWriter(path) {
  if (typeof path !== 'string' || !/^\/(?:[A-Za-z0-9_-]+\/)+public-ready\.json$/.test(path)) {
    throw Error('invalid isolated signal path');
  }
  const writer = `import os,sys
p=${JSON.stringify(path)}
data=sys.stdin.buffer.read(1025)
if not data or len(data)>1024: raise SystemExit('invalid signal length')
tmp=p+'.new'
fd=os.open(tmp,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
with os.fdopen(fd,'wb') as f:
 f.write(data)
 f.flush()
 os.fsync(f.fileno())
os.link(tmp,p)
os.unlink(tmp)
d=os.open(os.path.dirname(p),os.O_RDONLY|os.O_DIRECTORY)
try: os.fsync(d)
finally: os.close(d)
`;
  return `python3 -c 'import base64;exec(base64.b64decode("${Buffer.from(writer).toString('base64')}"))'`;
}
