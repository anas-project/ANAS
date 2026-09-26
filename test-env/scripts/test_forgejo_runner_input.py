"""Exercise the input protocol using private temporary paths, never guest /run.

Only the two literal file roots are relocated in a test copy. Coreutils reads,
exclusive creation, hashing and shell control flow are real. The independent
native gate must run the shipped files at their actual guest paths.
"""
import hashlib
from pathlib import Path
import os
import shutil
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[2] / 'modules/forgejo/runner-image/anas-forgejo-runner-input'
TOKEN = b'b' * 40
PUBLIC = b'-----BEGIN CERTIFICATE-----\nvalidated-public-fixture\n-----END CERTIFICATE-----\n'


class RunnerInputProtocol(unittest.TestCase):
    def invoke(self, payload, size=0, digest='', preexisting=False, expect_unread=False):
        with tempfile.TemporaryDirectory(prefix='anas-runner-input-') as directory:
            root = Path(directory)
            inputs = root / 'input'; inputs.mkdir(mode=0o700)
            roots = root / 'system-ca.pem'; roots.write_bytes(b'original-public-roots\n')
            script = SOURCE.read_text()
            self.assertEqual(script.count('token_dir=/run/anas-actions-token'), 1)
            self.assertEqual(script.count('system_roots=/etc/ssl/certs/ca-certificates.crt'), 1)
            script = script.replace('token_dir=/run/anas-actions-token', 'token_dir='+str(inputs))
            script = script.replace('system_roots=/etc/ssl/certs/ca-certificates.crt', 'system_roots='+str(roots))
            program = root/'input.sh'; program.write_text(script)
            commands = root/'bin'; commands.mkdir()
            for name in ('dd', 'sha256sum'):
                binary = shutil.which('g'+name) or shutil.which(name)
                self.assertIsNotNone(binary, 'required coreutils command cannot be skipped')
                (commands/name).symlink_to(binary)
            if preexisting:
                (inputs/'runner-token').symlink_to(roots)
            env = {'PATH': str(commands)+':/usr/bin:/bin', 'LC_ALL': 'C'}
            command = ['/bin/sh', str(program), str(size), digest]
            if expect_unread:
                # Retain our duplicate of the actual input pipe: an empty
                # output directory alone cannot prove the token was unread.
                reader, writer = os.pipe()
                try:
                    self.assertLess(len(payload), 4096)
                    self.assertEqual(os.write(writer, payload), len(payload))
                    os.close(writer); writer = None
                    result = subprocess.run(command, stdin=reader, env=env, capture_output=True, timeout=4)
                    self.assertEqual(os.read(reader, len(payload)+1), payload)
                finally:
                    os.close(reader)
                    if writer is not None:
                        os.close(writer)
            else:
                result = subprocess.run(command, input=payload, env=env, capture_output=True, timeout=4)
            self.assertNotIn(TOKEN, result.stdout+result.stderr)
            files = {p.name: p.read_bytes() for p in inputs.iterdir() if p.is_file() and not p.is_symlink()}
            self.assertEqual(roots.read_bytes(), b'original-public-roots\n')
            modes = {p.name: p.stat().st_mode & 0o777 for p in inputs.iterdir() if p.is_file() and not p.is_symlink()}
            return result.returncode, files, modes

    def test_legacy_token_has_no_projected_trust(self):
        code, files, modes = self.invoke(TOKEN)
        self.assertEqual(code, 0)
        self.assertEqual(files, {'runner-token': TOKEN})
        self.assertEqual(modes['runner-token'], 0o600)

    def test_public_trust_is_bound_and_added_without_replacing_system_roots(self):
        code, files, modes = self.invoke(TOKEN+PUBLIC, len(PUBLIC), hashlib.sha256(PUBLIC).hexdigest())
        self.assertEqual(code, 0)
        self.assertEqual(files['runner-token'], TOKEN)
        self.assertEqual(files['runner-ca-bundle.pem'], b'original-public-roots\n'+PUBLIC)
        self.assertEqual(set(files), {'runner-token', 'runner-ca-bundle.pem'})
        self.assertEqual(set(modes.values()), {0o600})

    def test_truncation_wrong_digest_or_trailing_payload_cannot_survive(self):
        for payload, size, digest in (
            (TOKEN[:-1], 0, ''), (TOKEN+b'extra', 0, ''),
            (TOKEN+PUBLIC[:-1], len(PUBLIC), hashlib.sha256(PUBLIC).hexdigest()),
            (TOKEN+PUBLIC, len(PUBLIC), 'a'*64),
            (TOKEN+PUBLIC+b'extra', len(PUBLIC), hashlib.sha256(PUBLIC).hexdigest()),
            (b'X'*40+PUBLIC, len(PUBLIC), hashlib.sha256(PUBLIC).hexdigest())):
            with self.subTest(size=size):
                code, files, _ = self.invoke(payload, size, digest)
                self.assertNotEqual(code, 0)
                self.assertEqual(files, {})

    def test_bad_frame_headers_never_consume_input(self):
        for size, digest in ((32769, 'a'*64), ('01', 'a'*64), (-1, 'a'*64), (1, ''), (0, 'a'*64), (10, 'A'*64)):
            with self.subTest(size=size, digest=digest):
                code, files, _ = self.invoke(TOKEN+PUBLIC, size, digest, expect_unread=True)
                self.assertEqual(code, 64)
                self.assertEqual(files, {})

    def test_preexisting_link_cannot_replace_a_file(self):
        code, _, _ = self.invoke(TOKEN, preexisting=True, expect_unread=True)
        self.assertNotEqual(code, 0)


if __name__ == '__main__':
    unittest.main()
