# TEST_CASES: MAILMOD-T-079
"""Submit a synthetic message to the isolated relay with verified STARTTLS."""
import json, smtplib, ssl, sys
config = json.load(sys.stdin)
context = ssl.create_default_context(cafile=config['cert_path'])
with smtplib.SMTP('127.0.0.1', config['port'], timeout=15) as client:
    client.ehlo()
    client.starttls(context=context)
    client.ehlo()
    for address in config['addresses']:
        failed = client.sendmail('sender@example.net', [address], config['message'].encode())
        if failed: raise RuntimeError('SMTP recipient rejected')
    try:
        client.sendmail('sender@example.net', [config['invalid_address']], config['message'].encode())
    except smtplib.SMTPRecipientsRefused:
        pass
    else:
        raise RuntimeError('unregistered SMTP recipient accepted')
print('submitted')
