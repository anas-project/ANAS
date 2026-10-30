# TEST_CASES: MAILMOD-T-009 MAILMOD-T-080
"""Run a bounded, disposable OSA tunnel to the finance test receiver."""
import hashlib, os, pathlib, re, select, subprocess, sys, time

if len(sys.argv) != 4 or not re.fullmatch(r'/tmp/anas-mail-notify-[a-f0-9]{12}', sys.argv[1]) or not re.fullmatch(r'[1-9][0-9]{0,4}', sys.argv[2]) or int(sys.argv[2]) > 65535 or not re.fullmatch(r'[a-f0-9]{64}', sys.argv[3]):
    raise SystemExit('invalid isolated tunnel arguments')
root = pathlib.Path(sys.argv[1])
binary = pathlib.Path('/tmp/anas-mail-cloudflared-test-20261030/cloudflared')
if root.is_symlink() or not root.is_dir() or root.stat().st_uid != os.getuid() or root.stat().st_mode & 0o077 or binary.is_symlink() or not binary.is_file() or hashlib.sha256(binary.read_bytes()).hexdigest() != sys.argv[3]:
    raise SystemExit('isolated tunnel files invalid')
for name in ('notify.crt','empty.yml'):
    p = root/name
    if p.is_symlink() or not p.is_file() or p.stat().st_uid != os.getuid() or p.stat().st_mode & 0o077: raise SystemExit('private tunnel input required')
proc = None
try:
    proc = subprocess.Popen([str(binary),'--config',str(root/'empty.yml'),'tunnel','--no-autoupdate','--metrics','127.0.0.1:0','--protocol','http2','--url','https://127.0.0.1:'+sys.argv[2],'--origin-ca-pool',str(root/'notify.crt')],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
    deadline = time.monotonic() + 1500
    while time.monotonic() < deadline and proc.poll() is None:
        ready,_,_ = select.select([sys.stdin,proc.stdout],[],[],1)
        if sys.stdin in ready:
            sys.stdin.readline(128)
            break
        if proc.stdout in ready:
            data = os.read(proc.stdout.fileno(),8192)
            if data:
                os.write(sys.stdout.fileno(),data)
finally:
    if proc is not None and proc.poll() is None:
        proc.terminate()
        try: proc.wait(timeout=15)
        except subprocess.TimeoutExpired: proc.kill(); proc.wait(timeout=5)
    if root.is_symlink() or root.stat().st_uid != os.getuid(): raise RuntimeError('unknown directory identity')
    for path in root.iterdir():
        if path.is_symlink() or not path.is_file(): raise RuntimeError('unexpected tunnel file')
        path.unlink()
    root.rmdir()
    print('{"state":"cleaned"}',flush=True)
