# TEST_CASES: MAILMOD-T-009
import json, os, pathlib, subprocess, tempfile, unittest
SCRIPT = pathlib.Path(__file__).with_name('verification-meta.py')
class TestVerificationMetadata(unittest.TestCase):
    def test_private_bounded_metadata_does_not_expose_link_or_address(self):
        with tempfile.TemporaryDirectory() as directory:
            path=pathlib.Path(directory)/'test.eml'
            path.write_text('From: Sender <fake@cloudflare.com>\nContent-Type: text/html; charset=utf-8\n\n<a href="https://dash.cloudflare.com/verify?token=SECRET">link</a>')
            path.chmod(0o600)
            r=subprocess.run(['python3',str(SCRIPT),str(path)],capture_output=True,text=True,check=True)
            self.assertEqual(json.loads(r.stdout), {'from_domains':['cloudflare.com'],'https_link_hosts':['dash.cloudflare.com'],'from_header_is_authenticated':False})
            self.assertNotIn('SECRET',r.stdout);self.assertNotIn('fake@',r.stdout)
    def test_nonprivate_file_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path=pathlib.Path(directory)/'test.eml';path.write_text('');path.chmod(0o644)
            self.assertNotEqual(subprocess.run(['python3',str(SCRIPT),str(path)],capture_output=True).returncode,0)
if __name__=='__main__': unittest.main()
