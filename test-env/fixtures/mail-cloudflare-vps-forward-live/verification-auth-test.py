# TEST_CASES: MAILMOD-T-009
"""Test binding checks with a fake verifier; not a cryptographic test."""
import json,os,pathlib,subprocess,tempfile,unittest
SCRIPT=pathlib.Path(__file__).with_name('verification-auth.py')
TARGET='anas-'+'a'*32+'@vps-target.mail-poc.bengbu.cc.cd'
class TestAuthenticatedHeaderBinding(unittest.TestCase):
    def invoke(self,*,extra='',recipient=TARGET,domain='notify.cloudflare.com',signed='from:to:subject',private=True):
        with tempfile.TemporaryDirectory() as root:
            root=pathlib.Path(root);(root/'dkim.py').write_text('class DKIM:\n def __init__(self, data): pass\n def verify(self, idx, dnsfunc): return True\n')
            msg=root/'message.eml';intent=root/'destination.json'
            msg.write_text('From: user@notify.cloudflare.com\nTo: '+recipient+'\nSubject: test\n'+extra+'DKIM-Signature: v=1; d='+domain+'; s=selector; h='+signed+'; b=synthetic\n\nbody\n')
            intent.write_text(json.dumps({'target':TARGET}));msg.chmod(0o600 if private else 0o644);intent.chmod(0o600)
            env=os.environ.copy();env['PYTHONPATH']=str(root)
            return subprocess.run(['python3',str(SCRIPT),str(msg),str(intent)],capture_output=True,text=True,env=env)
    def test_matched_binding_does_not_print_address(self):
        r=self.invoke();self.assertEqual(r.returncode,0);self.assertNotIn(TARGET,r.stdout)
    def test_duplicate_headers_and_wrong_bindings_rejected(self):
        for options in ({'extra':'From: intruder@example.test\n'}, {'extra':'To: other@example.test\n'}, {'extra':'Subject: other\n'}, {'recipient':'other@example.test'}, {'domain':'foreign.example'}, {'signed':'to:subject'}, {'private':False}):
            with self.subTest(options=options):self.assertNotEqual(self.invoke(**options).returncode,0)
if __name__=='__main__':unittest.main()
