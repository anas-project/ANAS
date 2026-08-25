#!/usr/bin/env python3

import hashlib
import hmac
import importlib.util
from pathlib import Path
import unittest


MODULE_PATH = Path(__file__).with_name("server-vikunja-webhook-receiver.py")
SPEC = importlib.util.spec_from_file_location("vikunja_webhook_receiver", MODULE_PATH)
assert SPEC and SPEC.loader
RECEIVER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RECEIVER)


class SignatureTest(unittest.TestCase):
    def test_accepts_exact_hmac_sha256_hex(self) -> None:
        secret = b"test-only-secret"
        body = b'{"event_name":"task.created"}'
        signature = hmac.new(secret, body, hashlib.sha256).hexdigest()
        self.assertTrue(RECEIVER.signature_is_valid(secret, body, signature))

    def test_rejects_wrong_signature_and_modified_body(self) -> None:
        secret = b"test-only-secret"
        body = b'{"event_name":"task.created"}'
        signature = hmac.new(secret, body, hashlib.sha256).hexdigest()
        self.assertFalse(RECEIVER.signature_is_valid(secret, body, "0" * 64))
        self.assertFalse(RECEIVER.signature_is_valid(secret, body + b" ", signature))


if __name__ == "__main__":
    unittest.main()
