# TEST_CASES: MAILMOD-T-009
import pathlib, subprocess, tempfile, unittest
from email.message import EmailMessage
from email.policy import SMTP
SCRIPT=pathlib.Path(__file__).with_name('verify-public.py')
NONCE='a'*32
class TestPublicMessage(unittest.TestCase):
    def verify(self,*,good_attachment=True,good_to=True,private=True,controlled_to=False,resend=False,wrong_subject=False,wrong_body=False,duplicate_id=False):
        with tempfile.TemporaryDirectory() as directory:
            path=pathlib.Path(directory)/'public.eml'
            m=EmailMessage(policy=SMTP);m['From']='probe@mail-poc.hlong.wang'
            target='cf-vps-test-abcdef123456@mail-poc.bengbu.cc.cd'
            m['To']=(target if resend else ('whlsxl@126.com' if controlled_to else 'misleading@foreign.example')) if good_to else 'other@example.test'
            m['Subject']='ANAS isolated forwarding '+('b'*32 if wrong_subject else NONCE)
            m['Message-ID']='<provider-generated@example.test>' if resend else '<anas-forward-'+NONCE+'@mail-poc.hlong.wang>'
            m.set_content('isolated-test-body '+('b'*32 if wrong_body else NONCE))
            m.add_attachment(bytes([0,1,2,255]) if good_attachment else b'changed',maintype='application',subtype='octet-stream',filename='proof.bin')
            raw=m.as_bytes()
            if duplicate_id: raw=b'Message-ID: <other@example.test>\r\n'+raw
            path.write_bytes(raw);path.chmod(0o600 if private else 0o644)
            args=['resend',target] if resend else (['whlsxl@126.com'] if controlled_to else [])
            return subprocess.run(['python3',str(SCRIPT),str(path),NONCE]+args,capture_output=True,text=True)
    def test_matches_original_and_does_not_print_message(self):
        result=self.verify();self.assertEqual(result.returncode,0);self.assertNotIn(NONCE,result.stdout);self.assertNotIn('probe@',result.stdout)
    def test_rejects_changed_attachment_forged_to_or_insecure_file(self):
        for options in ({'good_attachment':False},{'good_to':False},{'private':False}):
            with self.subTest(options=options):self.assertNotEqual(self.verify(**options).returncode,0)
    def test_controlled_external_to_is_verified(self):
        self.assertEqual(self.verify(controlled_to=True).returncode,0)
        self.assertNotEqual(self.verify(controlled_to=True,good_to=False).returncode,0)
    def test_resend_can_replace_id_and_recipient_without_weakening_content_checks(self):
        self.assertEqual(self.verify(resend=True).returncode,0)
        for options in ({'good_to':False},{'good_attachment':False},{'wrong_subject':True},{'wrong_body':True},{'duplicate_id':True}):
            with self.subTest(options=options):self.assertNotEqual(self.verify(resend=True,**options).returncode,0)
if __name__=='__main__':unittest.main()
