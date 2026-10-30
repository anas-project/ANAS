# TEST_CASES: MAILMOD-T-010
import json, pathlib, socket, subprocess, tempfile, threading, unittest

SCRIPT = str(pathlib.Path(__file__).with_name('mail-cloudflare-rcpt-check.py'))

class ProbeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.binding = pathlib.Path(self.tmp.name) / 'binding.json'
        self.binding.write_text(json.dumps({'Routes': [{'address': f'smtp-123456abcdef-{n}@mail-poc.bengbu.cc.cd'} for n in ('main', 'alias', 'extra')]}))
        self.binding.chmod(0o600)

    def tearDown(self):
        self.tmp.cleanup()

    def test_socks_restriction_and_error(self):
        listener = socket.socket()
        listener.bind(('127.0.0.1', 0))
        listener.settimeout(5)
        listener.listen(1)
        port = listener.getsockname()[1]
        seen = []
        def serve():
            conn, _ = listener.accept()
            with conn:
                conn.settimeout(5)
                def exact(n):
                    data = b''
                    while len(data) < n:
                        part = conn.recv(n - len(data))
                        if not part:
                            raise OSError('unexpected EOF')
                        data += part
                    return data
                seen.append(exact(3))
                conn.sendall(b'\x05\x00')
                header = exact(5)
                length = header[4]
                rest = exact(length + 2)
                seen.append(header + rest)
                conn.sendall(b'\x05\x05\x00\x01')
            listener.close()
        worker = threading.Thread(target=serve)
        worker.start()
        result = subprocess.run(['python3', SCRIPT, str(self.binding), 'route1.mx.cloudflare.net', str(port)], capture_output=True, text=True, timeout=10)
        worker.join(10)
        self.assertFalse(worker.is_alive())
        self.assertEqual(result.returncode, 1)
        self.assertEqual(json.loads(result.stdout), {'phase': 'connect', 'codes': [], 'error': 'OSError'})
        self.assertEqual(seen, [b'\x05\x01\x00', b'\x05\x01\x00\x03\x18route1.mx.cloudflare.net\x00\x19'])

    def test_target_and_port_rejected_before_connect(self):
        for host, port in [('example.com', '17142'), ('route1.mx.cloudflare.net', '65536'), ('route1.mx.cloudflare.net', '-1')]:
            result = subprocess.run(['python3', SCRIPT, str(self.binding), host, port], capture_output=True, text=True, timeout=5)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(result.stdout, '')

if __name__ == '__main__':
    unittest.main()
