# TEST_CASES: MAILMOD-T-007
"""Small, serial, disk-bounded proof store for the disposable experiment.

This is not a product receipt store. Process crashes are recoverable; a started
delivery without durable completion remains unknown and is never resent.
"""
import contextlib
import fcntl
import json
import os
import re
from pathlib import Path

from copy_binding import MAX_BYTES, bind_delivery, split_message


class CopyStore:
    # Two bounded message copies, metadata, markers, and one atomic-write temp.
    RESERVATION = 3 * MAX_BYTES + 8192

    def __init__(self, root, *, max_intents=8, max_bytes=8 * RESERVATION, checkpoint=None):
        self.root = Path(root)
        self.root.mkdir(mode=0o700, exist_ok=True)
        st = self.root.lstat()
        if self.root.is_symlink() or not self.root.is_dir() or st.st_uid != os.getuid() or st.st_mode & 0o077:
            raise ValueError('private proof directory required')
        self.max_intents, self.max_bytes = max_intents, max_bytes
        self.checkpoint = checkpoint or (lambda phase: None)

    @contextlib.contextmanager
    def guard(self):
        # A single process-independent lock keeps this bounded experiment small.
        # Native delivery is serialized; this is not a production throughput test.
        fd = os.open(self.root / '.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        try:
            fcntl.flock(fd, fcntl.LOCK_EX)
            yield
        finally:
            os.close(fd)

    @staticmethod
    def sync_dir(path):
        fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)

    def path(self, key):
        if not re.fullmatch(r'[a-f0-9]{64}', key):
            raise ValueError('invalid private intent')
        return self.root / key

    def save(self, path, name, value):
        if len(value) > MAX_BYTES:
            raise ValueError('proof exceeds message bound')
        target, temporary = path / name, path / ('.' + name + '.tmp')
        if target.exists():
            if target.read_bytes() != value:
                raise ValueError('immutable proof conflict')
            # A previous process may have died after publishing the name but
            # before syncing its directory. Finish that step before continuing.
            self.sync_dir(path)
            return
        # Only this process can hold guard(); an interrupted temp is disposable.
        temporary.unlink(missing_ok=True)
        with temporary.open('xb') as f:
            os.fchmod(f.fileno(), 0o600)
            f.write(value)
            f.flush()
            os.fsync(f.fileno())
        self.checkpoint(name + '.before_commit')
        os.link(temporary, target)
        self.checkpoint(name + '.after_commit')
        temporary.unlink()
        self.sync_dir(path)
        self.checkpoint(name + '.durable')

    def prepare(self, key, target, filtered, received):
        path = self.path(key)
        if not re.fullmatch(r'mbx_[a-f0-9]{32}@mailboxes\.anas\.invalid', target):
            raise ValueError('unexpected private target')
        split_message(filtered)
        # Validate the privately generated header, including the final size.
        bind_delivery(b'Received: discarded local trace\r\n' + filtered, filtered, received)
        info = json.dumps({'target': target, 'received': received.decode('ascii')}, sort_keys=True).encode()
        with self.guard():
            if not path.exists():
                count = sum(p.is_dir() for p in self.root.iterdir())
                if count >= self.max_intents or (count + 1) * self.RESERVATION > self.max_bytes:
                    raise ValueError('private proof capacity exhausted')
                path.mkdir(mode=0o700)
                self.sync_dir(self.root)
            self.save(path, 'info', info)
            self.save(path, 'filtered.eml', filtered)
            self.save(path, 'ready', b'verified\n')

    def deliver(self, key, target, incoming, send):
        path = self.path(key)
        with self.guard():
            try:
                if (path / 'ready').read_bytes() != b'verified\n':
                    return 451
                info = json.loads((path / 'info').read_bytes())
                if set(info) != {'target', 'received'}:
                    return 451
                if target != info['target']:
                    return 550
                filtered = (path / 'filtered.eml').read_bytes()
                try:
                    delivered = bind_delivery(incoming, filtered, info['received'].encode('ascii'))
                except ValueError:
                    return 550
                if (path / 'completed').exists():
                    if (path / 'completed').read_bytes() != b'processed\n' or (path / 'started').read_bytes() != b'unknown\n' or (path / 'delivery.eml').read_bytes() != delivered:
                        return 451
                    self.sync_dir(path)
                    return 250
                if (path / 'started').exists():
                    return 451
                self.save(path, 'delivery.eml', delivered)
                self.save(path, 'started', b'unknown\n')
                self.checkpoint('before_native')
                if send(target, delivered) != 250:
                    return 451
                self.checkpoint('after_native')
                self.save(path, 'completed', b'processed\n')
                self.checkpoint('before_response')
                return 250
            except (OSError, ValueError, KeyError, TypeError):
                # Corrupt/incomplete private facts cannot produce a completion.
                return 451
