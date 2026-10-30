# TEST_CASES: MAILMOD-T-007
import multiprocessing
import os
import signal
import tempfile
import unittest
from unittest import mock
from pathlib import Path

from copy_store import CopyStore

KEY = 'a' * 64
TARGET = 'mbx_' + 'a' * 32 + '@mailboxes.anas.invalid'
FILTERED = b'Subject: synthetic\r\n\r\nbody\r\n'
INCOMING = b'Received: untrusted trace\r\n' + FILTERED
RECEIVED = b'Received: by anas-controlled-intake (private intent); Thu, 01 Jan 2026 00:00:00 +0000'


def record_send(root, target, data):
    if target != TARGET or data != RECEIVED + b'\r\n' + FILTERED:
        raise ValueError('wrong delivered content or target')
    with open(Path(root).parent / 'native-events', 'ab', buffering=0) as f:
        f.write(b'accepted\n')
        os.fsync(f.fileno())
    return 250


def attempt(root, phase, pipe):
    def checkpoint(current):
        if current == phase:
            pipe.send(current)
            # The parent kills this actual process at the reported boundary.
            pipe.recv()
    store = CopyStore(root, checkpoint=checkpoint)
    store.prepare(KEY, TARGET, FILTERED, RECEIVED)
    pipe.send(store.deliver(KEY, TARGET, INCOMING, lambda target, data: record_send(root, target, data)))


class DurableCopies(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name) / 'proof'
        self.store = CopyStore(self.root)
        self.context = multiprocessing.get_context('spawn')

    def events(self):
        p = self.root.parent / 'native-events'
        return len(p.read_bytes().splitlines()) if p.exists() else 0

    def deliver(self, store=None, incoming=INCOMING, target=TARGET):
        return (store or self.store).deliver(KEY, target, incoming, lambda t, d: record_send(self.root, t, d))

    def test_restart_and_completed_replay(self):
        self.store.prepare(KEY, TARGET, FILTERED, RECEIVED)
        self.assertEqual(self.deliver(), 250)
        self.assertEqual(self.deliver(CopyStore(self.root)), 250)
        self.assertEqual(self.events(), 1)

    def test_unknown_does_not_redeliver(self):
        self.store.prepare(KEY, TARGET, FILTERED, RECEIVED)
        self.assertEqual(self.store.deliver(KEY, TARGET, INCOMING, lambda t, d: 451), 451)
        self.assertEqual(self.deliver(CopyStore(self.root)), 451)
        self.assertEqual(self.events(), 0)

    def test_forgery_rejected_even_after_completion(self):
        self.store.prepare(KEY, TARGET, FILTERED, RECEIVED)
        self.assertEqual(self.deliver(), 250)
        self.assertEqual(self.deliver(incoming=INCOMING.replace(b'body', b'changed')), 550)
        self.assertEqual(self.deliver(target=TARGET.replace('mbx_a', 'mbx_b')), 550)
        self.assertEqual(self.events(), 1)

    def test_capacity_and_immutable_preparation(self):
        store = CopyStore(self.root, max_intents=1)
        store.prepare(KEY, TARGET, FILTERED, RECEIVED)
        store.prepare(KEY, TARGET, FILTERED, RECEIVED)
        with self.assertRaises(ValueError):
            store.prepare('b' * 64, TARGET, FILTERED, RECEIVED)
        with self.assertRaises(ValueError):
            store.prepare(KEY, TARGET, FILTERED.replace(b'body', b'new'), RECEIVED)
        empty = CopyStore(self.root.parent / 'too-small', max_bytes=CopyStore.RESERVATION-1)
        with self.assertRaises(ValueError):
            empty.prepare(KEY, TARGET, FILTERED, RECEIVED)
        self.assertEqual(self.deliver(), 250)

    def test_private_files_and_incomplete_or_corrupt_proof(self):
        self.assertEqual(self.deliver(), 451)
        self.store.prepare(KEY, TARGET, FILTERED, RECEIVED)
        for p in self.root.rglob('*'):
            self.assertEqual(p.stat().st_mode & 0o077, 0)
        self.assertEqual(self.deliver(), 250)
        (self.root / KEY / 'completed').write_bytes(b'corrupt\n')
        self.assertEqual(self.deliver(CopyStore(self.root)), 451)
        self.assertEqual(self.events(), 1)

    def test_sync_failure_never_acknowledges_or_repeats_delivery(self):
        self.store.prepare(KEY, TARGET, FILTERED, RECEIVED)
        with mock.patch.object(self.store, 'sync_dir', side_effect=OSError('synthetic sync failure')):
            self.assertEqual(self.deliver(), 451)
        self.assertEqual(self.events(), 0)
        self.assertEqual(self.deliver(), 250)
        with mock.patch.object(self.store, 'sync_dir', side_effect=OSError('synthetic sync failure')):
            self.assertEqual(self.deliver(), 451)
        self.assertEqual(self.events(), 1)

    def test_concurrent_processes_deliver_once(self):
        processes = []
        try:
            for _ in range(4):
                parent, child = self.context.Pipe()
                process = self.context.Process(target=attempt, args=(self.root, None, child))
                process.start()
                processes.append((process, parent))
            for process, pipe in processes:
                self.assertTrue(pipe.poll(15), 'concurrent delivery timed out')
                self.assertEqual(pipe.recv(), 250)
                process.join(5)
                self.assertEqual(process.exitcode, 0)
            self.assertEqual(self.events(), 1)
        finally:
            for process, pipe in processes:
                if process.is_alive():
                    process.kill()
                process.join(5)
                pipe.close()

    def test_real_process_kill_at_each_persistence_boundary(self):
        phases = [name+'.'+stage for name in ('info', 'filtered.eml', 'ready', 'delivery.eml', 'started', 'completed') for stage in ('before_commit', 'after_commit', 'durable')]
        phases += ['before_native', 'after_native', 'before_response']
        for phase in phases:
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as directory:
                root = Path(directory) / 'proof'
                parent, child = self.context.Pipe()
                process = self.context.Process(target=attempt, args=(root, phase, child))
                process.start()
                try:
                    self.assertTrue(parent.poll(15), phase+' was not reached')
                    self.assertEqual(parent.recv(), phase)
                    process.kill()
                    process.join(5)
                    self.assertEqual(process.exitcode, -signal.SIGKILL)
                    recovered = CopyStore(root)
                    recovered.prepare(KEY, TARGET, FILTERED, RECEIVED)
                    started = (root / KEY / 'started').exists()
                    completed = (root / KEY / 'completed').exists()
                    expected = 250 if completed or not started else 451
                    self.assertEqual(recovered.deliver(KEY, TARGET, INCOMING, lambda t, d: record_send(root, t, d)), expected)
                    events = root.parent / 'native-events'
                    count = len(events.read_bytes().splitlines()) if events.exists() else 0
                    self.assertLessEqual(count, 1)
                    if expected == 250:
                        self.assertEqual(count, 1)
                finally:
                    if process.is_alive():
                        process.kill()
                    process.join(5)
                    parent.close()


if __name__ == '__main__':
    unittest.main()
