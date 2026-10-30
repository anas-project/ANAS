// TEST_CASES: LLNG-READY-T-005
import {execFileSync} from 'node:child_process';
import {readFileSync} from 'node:fs';
import {expect, test} from '@playwright/test';

const app = 'https://tasks.llngtest.invalid:18443';
const credential = JSON.parse(readFileSync(process.env.ANAS_TEST_CREDENTIAL_FILE, 'utf8')).portal.portalnormal;
function fixture(action) {
  if (process.env.ANAS_TEST_SSH_HOST !== 'whl@finance.hlong.wang') throw new Error('isolated finance fixture required');
  return JSON.parse(execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10',
    process.env.ANAS_TEST_SSH_HOST, 'sudo', 'python3', '/home/whl/anas-llng-ready-test-20261023/llng-service-rotation-fixture.py', action],
    {encoding: 'utf8', timeout: 120_000, stdio: 'pipe'}));
}

async function login(browser) {
  const context = await browser.newContext({ignoreHTTPSErrors: true});
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  try {
    const page = await context.newPage();
    page.setDefaultTimeout(30_000); page.setDefaultNavigationTimeout(60_000);
    await page.goto(app);
    const user = page.locator('input[name=user]').first(); await user.fill(credential.username);
    await page.locator('input[name=password]').first().fill(credential.password);
    await page.locator('form').filter({has: user}).locator('[type=submit]').first().click({noWaitAfter: true});
    // Application authentication is established by its user API below; a slow
    // unrelated page asset must not turn a completed callback into a timeout.
    await expect.poll(() => new URL(page.url()).origin, {timeout: 60_000}).toBe(app);
    const current = () => page.evaluate(async () => {
      const token = localStorage.getItem('token');
      if (!token) return 0;
      const r = await fetch('/api/v1/user', {headers: {Authorization: 'Bearer ' + token}});
      return r.ok ? (await r.json()).id : 0;
    }).catch(() => 0);
    await expect.poll(current, {timeout: 45_000}).toBeGreaterThan(0);
    return current();
  } finally {await context.close();}
}

test('native LDAP and SCRAM database rotation reject retired secrets and preserve actual application identity', async ({browser}) => {
  const anchor = await login(browser);
  let active = false;
  try {
    // Roll back even if a resumed fixture fails before activation, because it
    // temporarily sets the disposable DC's previous-password grace period.
    active = process.env.ANAS_TEST_SERVICE_ROTATION_PREPARED === '1';
    fixture(process.env.ANAS_TEST_SERVICE_ROTATION_PREPARED === '1' ? 'resume' : 'prepare');
    console.log('Dedicated service credential baseline ready');
    let probe = fixture('probe');
    expect(probe).toMatchObject({ldap_old: true, ldap_new: false, db_old: true, db_new: false});
    active = true; fixture('activate');
    expect(await login(browser)).toBe(anchor);
    console.log('Actual application login with dedicated LDAP/database credentials passed');
    fixture('rotate-ldap');
    probe = fixture('probe');
    expect(probe).toMatchObject({ldap_old: true, ldap_new: true, db_old: true, db_new: false});
    await expect.poll(() => fixture('probe').ldap_old, {timeout: 90_000, intervals: [5000]}).toBe(false);
    probe = fixture('probe');
    expect(probe).toMatchObject({ldap_old: false, ldap_new: true, db_old: true, db_new: false});
    console.log('Previous LDAP password rejected after the one-minute window');
    fixture('apply-ldap');
    expect(await login(browser)).toBe(anchor);
    console.log('Replacement LDAP credential accepted by actual application login');
    fixture('rotate-db');
    probe = fixture('probe');
    expect(probe).toMatchObject({ldap_old: false, ldap_new: true, db_old: false, db_new: true});
    const before = probe.oidc_rows;
    fixture('apply-db');
    expect(await login(browser)).toBe(anchor);
    expect(fixture('probe').oidc_rows).toBeGreaterThan(before);
    console.log('Replacement database credential and new session writes verified');
    fixture('rollback'); active = false;
    expect(await login(browser)).toBe(anchor);
    console.log('LDAPS accepted the previous password only during the measured one-minute grace period; retired LDAPS/SCRAM secrets were rejected, real app identity/new database writes passed, original configuration and Samba grace restored');
  } finally {if (active) fixture('rollback');}
});
