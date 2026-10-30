# TEST_CASES: MAILMOD-T-007
import struct
import unittest

from copy_binding import MAX_BYTES, bind_delivery, reconstruct, split_message


class CopyBinding(unittest.TestCase):
    def setUp(self):
        self.original = b'Received: from original.example\r\nX-Duplicate:\tfirst\r\n\tcontinued\r\nX-Duplicate: second\r\nSubject: original\r\n\r\n.dot\r\nbody\r\n'
        self.headers, self.body = split_message(self.original)
        self.frames = [name + b'\0' + value.replace(b'\r\n', b'\n') + b'\0' for name, value in (h.split(b':', 1) for h in self.headers)]
        self.received = b'Received: by anas-controlled-intake (private intent); Thu, 01 Jan 2026 00:00:00 +0000'

    def test_exact_input_and_filter_edits(self):
        changes = [('m', struct.pack('!I', 2) + b'X-Duplicate\0changed\0'), ('m', struct.pack('!I', 1) + b'Subject\0\0'), ('h', b'X-Filtered\0yes\0'), ('b', b'new '), ('b', b'body\r\n')]
        result = reconstruct(self.original, self.frames, self.body, changes)
        self.assertEqual(result, b'Received: from original.example\r\nX-Duplicate:\tfirst\r\n\tcontinued\r\nX-Duplicate: changed\r\nX-Filtered: yes\r\n\r\nnew body\r\n')

    def test_untrusted_extra_received_is_not_forwarded(self):
        incoming = b'Received: forged value must not reach Sieve\r\n' + self.original
        result = bind_delivery(incoming, self.original, self.received)
        self.assertEqual(result, self.received+b'\r\n'+self.original)
        self.assertNotIn(b'forged', result)

    def test_content_changes_and_extra_headers_are_rejected(self):
        incoming = b'Received: local transport\r\n' + self.original
        for bad in (incoming.replace(b'body', b'evil'), incoming.replace(b'Subject: original', b'Subject: other'), incoming.replace(b'Received: from original.example', b'Received: from forged.example'), b'X-Extra: injected\r\n'+incoming, b'Received: extra\r\n'+incoming, self.original):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                bind_delivery(bad, self.original, self.received)

    def test_ambiguous_or_oversized_headers_fail_closed(self):
        for value in (b'\tcontinued\r\n\r\nbody', b'Bad Name: value\r\n\r\nbody', b'Subject: one\nBcc: injected\r\n\r\nbody', b'Subject: ok\r\n\r\n'+b'x'*MAX_BYTES):
            with self.subTest(value=value[:40]), self.assertRaises(ValueError):
                split_message(value)
        with self.assertRaises(ValueError):
            bind_delivery(b'Received: local\r\n'+self.original, self.original, self.received+b'\r\nBcc: injected@example.test')

    def test_input_forgery_or_unmapped_filter_operation_is_rejected(self):
        for body, changes in ((b'changed', []), (self.body, [('+', b'new@example.test\0')]), (self.body, [('i', struct.pack('!I', 0)+b'X-New\0value\0')]), (self.body, [('h', b'X-ANAS-Ingress-Key\0forged\0')]), (self.body, [('h', b'X-Injected\0good\nBcc: attacker@example.test\0')])):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                reconstruct(self.original, self.frames, body, changes)


if __name__ == '__main__':
    unittest.main()
