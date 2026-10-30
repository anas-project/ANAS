// TEST_CASES: LLNG-READY-T-005
import {execFileSync} from 'node:child_process';
import {readFileSync} from 'node:fs';
import {expect, test} from '@playwright/test';

const cloud = 'https://cloud.llngtest.invalid:18443';
const credential = JSON.parse(readFileSync(process.env.ANAS_TEST_CREDENTIAL_FILE, 'utf8')).portal.portalnormal;
function rotate(action) {
  if (process.env.ANAS_TEST_SSH_HOST !== 'whl@finance.hlong.wang') throw new Error('isolated finance fixture required');
  const result = execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10',
    process.env.ANAS_TEST_SSH_HOST, 'sudo', 'python3', '/home/whl/anas-llng-ready-test-20261023/llng-client-rotation-fixture.py', action],
    {encoding: 'utf8', timeout: 60_000, stdio: 'pipe'});
  return JSON.parse(result);
}
async function login(browser, accepted) {
  const context = await browser.newContext({ignoreHTTPSErrors: true});
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  try {
    const page = await context.newPage();
    await page.goto(cloud + '/apps/user_oidc/login/1');
    const user = page.locator('input[name=user]').first(); await user.fill(credential.username);
    await page.locator('input[name=password]').first().fill(credential.password);
    const callback = page.waitForResponse(r => new URL(r.url()).origin === cloud && new URL(r.url()).pathname === '/apps/user_oidc/code');
    await page.locator('form').filter({has: user}).locator('[type=submit]').first().click({noWaitAfter: true});
    const status = (await callback).status();
    await expect.poll(() => new URL(page.url()).origin, {timeout: 60_000}).toBe(cloud);
    const current = async () => {
      const cookie = (await context.cookies(cloud)).map(c => c.name + '=' + c.value).join('; ');
      const r = await context.request.get('https://127.0.0.1:18443/ocs/v2.php/cloud/user?format=json',
        {headers: {Host: 'cloud.llngtest.invalid:18443', Cookie: cookie, 'OCS-APIRequest': 'true'}, maxRedirects: 0});
      return {status: r.status(), id: (await r.json().catch(() => ({}))).ocs?.data?.id || ''};
    };
    if (!accepted) {
      expect(status).toBe(403);
      expect(await current()).toEqual({status: 401, id: ''});
      return '';
    }
    await expect.poll(async () => (await current()).id, {timeout: 60_000}).toMatch(/^[0-9a-f-]{36}$/);
    return (await current()).id;
  } finally {await context.close();}
}

test('native confidential client rotation rejects mismatched and retired credentials and supports rollback', async ({browser}) => {
  const anchor = await login(browser, true);
  let prepared = false;
  try {
    rotate('prepare'); prepared = true;
    let probe = rotate('probe');
    expect(probe.original.error).toBe('invalid_grant');
    expect(probe.replacement.error).toBe('invalid_client');
    rotate('provider');
    probe = rotate('probe');
    expect(probe.original.error).toBe('invalid_client');
    expect(probe.replacement.error).toBe('invalid_grant');
    await login(browser, false);
    rotate('consumer');
    expect(await login(browser, true)).toBe(anchor);
    rotate('rollback'); prepared = false;
    probe = rotate('probe');
    expect(probe.original.error).toBe('invalid_grant');
    expect(probe.replacement.error).toBe('invalid_client');
    expect(await login(browser, true)).toBe(anchor);
    console.log('Actual Nextcloud refused mismatched client credentials, resumed with replacement, rejected retired secret and preserved identity after rollback');
  } finally {if (prepared) rotate('rollback');}
});
