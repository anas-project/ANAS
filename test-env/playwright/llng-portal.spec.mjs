// TEST_CASES: LLNG-READY-T-001
import { readFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { expect, test } from '@playwright/test';

const portal = 'https://portal.llngtest.invalid:18443';
const credentials = JSON.parse(readFileSync(process.env.ANAS_TEST_CREDENTIAL_FILE, 'utf8')).portal;

async function api(page, path, body) {
  return page.evaluate(async ({path, body}) => {
    const bootstrap = await fetch('/api/bootstrap', {cache: 'no-store'}).then(r => r.json());
    const response = await fetch(path, {method: body === undefined ? 'GET' : 'POST', cache: 'no-store',
      headers: {'Content-Type': 'application/json', 'X-CSRF-Token': bootstrap.csrf},
      body: body === undefined ? undefined : JSON.stringify(body)});
    return {status: response.status, data: await response.json()};
  }, {path, body});
}
async function password(page, credential) {
  const user = page.locator('input[name="user"]').first();
  await expect(user).toBeVisible();
  await user.fill(credential.username);
  await page.locator('input[name="password"]').first().fill(credential.password);
  await page.locator('form').filter({has: user}).locator('[type="submit"]').first().click();
  await page.waitForURL(url => url.origin === portal && url.pathname === '/', {waitUntil: 'domcontentloaded'});
}

test('actual user_portal with LLNG: account, application policy and fresh OIDC interaction', async ({page, context}) => {
  page.setDefaultTimeout(30_000);
  await expect.poll(async () => (await context.request.get('https://127.0.0.1:18443/health',
    {headers: {Host: 'portal.llngtest.invalid:18443'}})).status(), {timeout: 45_000}).toBe(200);
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  await page.goto(portal + '/login');
  await password(page, credentials.portalnormal);
  const me = await api(page, '/api/me');
  expect(me.status).toBe(200);
  expect(me.data.person.username).toBe('portalnormal');
  expect(me.data.profile_editable).toBe(true);
  expect(me.data.administration).toBe(false);
  const apps = await api(page, '/api/apps');
  expect(apps.status).toBe(200);
  expect(apps.data.groups.flatMap(g => g.categories.flatMap(c => c.applications)).map(a => a.name)).toContain('vikunja');
  // The configured thirty-second fixture window makes a fresh signed auth_time
  // observable; no browser clock or server authentication timestamp is forged.
  await expect.poll(async () => (await api(page, '/api/me')).data.recent_authentication,
    {timeout: 40_000, intervals: [2000]}).toBe(false);
  await page.evaluate(async () => {
    const bootstrap = await fetch('/api/bootstrap').then(r => r.json());
    const form = document.createElement('form'); form.method = 'POST'; form.action = '/api/reauth';
    const csrf = document.createElement('input'); csrf.name = 'csrf'; csrf.value = bootstrap.csrf;
    form.append(csrf); document.body.append(form); form.submit();
  });
  await password(page, credentials.portalnormal);
  expect((await api(page, '/api/me')).data.recent_authentication).toBe(true);
  const users = await api(page, '/api/users');
  expect(users.status).toBe(200);
  expect(users.data.count).toBe(0);
  expect(users.data.users).toHaveLength(0);
  console.log('Actual portal account, application intersection, fresh OIDC interaction and ordinary-user boundary passed');
});

function ssh(args) {
  if (process.env.ANAS_TEST_SSH_HOST !== 'whl@finance.hlong.wang') throw new Error('explicit finance test host required');
  return execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10',
    process.env.ANAS_TEST_SSH_HOST, ...args], {timeout: 60_000, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe']});
}
function directory(action, user) {
  return ssh(['sudo', 'docker', '-H', 'unix:///run/anas-llng-test-1023.sock', 'exec', 'anas_llng_test_1023_samba',
    'python3', '/fixture/llng-directory-fixture.py', action, user]);
}
function eventStatus() {
  return JSON.parse(ssh(['sudo', 'python3', '/home/whl/anas-llng-ready-test-20261023/llng-event-status.py']));
}

function eventFault(action) {
  return ssh(['sudo', 'python3', '/home/whl/anas-llng-ready-test-20261023/llng-event-fixture.py', action]);
}

for (const boundary of ['locked', 'admin-role']) {
  test(`actual directory ${boundary} revokes the old portal cookie after access is restored`, async ({browser}) => {
    const contexts = [];
    async function login(name) {
      const context = await browser.newContext({ignoreHTTPSErrors: true}); contexts.push(context);
      await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
      const page = await context.newPage(); await page.goto(portal + '/login'); await password(page, credentials[name]);
      expect((await api(page, '/api/me')).status).toBe(200);
      const cookie = (await context.cookies(portal)).map(c => c.name + '=' + c.value).join('; ');
      await page.goto('about:blank');
      return {context, cookie};
    }
    directory('enable', 'portalnormal'); directory('unlocked', 'portalnormal');
    if (boundary === 'admin-role') directory('add_admin', 'portalnormal');
    try {
      const affected = await login('portalnormal'), peer = await login('portalother');
      const before = eventStatus().producer.last_published_seq;
      directory(boundary === 'locked' ? 'locked' : 'remove_admin', 'portalnormal');
      await expect.poll(() => {
        const s = eventStatus();
        return s.producer.last_published_seq > before && s.consumer.pending === 0
          && Number(s.cursor.cursor) >= s.producer.last_published_seq;
      }, {timeout: 60_000, intervals: [2000]}).toBe(true);
      directory(boundary === 'locked' ? 'unlocked' : 'add_admin', 'portalnormal');
      const status = async value => (await value.context.request.get('https://127.0.0.1:18443/api/me',
        {headers: {Host: 'portal.llngtest.invalid:18443', Cookie: value.cookie}})).status();
      expect(await status(affected)).toBe(401);
      expect(await status(peer)).toBe(200);
      console.log('Real ' + boundary + ' event revoked old cookie; restoring directory access did not revive it; peer unaffected');
    } finally {
      directory('unlocked', 'portalnormal');
      if (boundary === 'admin-role') directory('remove_admin', 'portalnormal');
      for (const context of contexts) await context.close();
    }
  });
}

for (const origin of ['application', 'provider', 'unavailable-provider']) {
  test(`actual portal OIDC ${origin} logout invalidates old cookies with a verified callback`, async ({browser}) => {
    const contexts = [];
    async function login(name) {
      const context = await browser.newContext({ignoreHTTPSErrors: true}); contexts.push(context);
      await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
      const page = await context.newPage(); await page.goto(portal + '/login');
      await password(page, credentials[name]);
      const cookie = (await context.cookies(portal)).map(c => c.name + '=' + c.value).join('; ');
      const iamCookies = await context.cookies('https://auth.llngtest.invalid:18443');
      return {context, page, cookie, iamCookies};
    }
    try {
      const affected = await login('portalnormal'), peer = await login('portalother');
      await peer.page.goto('about:blank');
      let privateCalls = 0, statePresent = false;
      affected.page.on('request', request => {
        const url = new URL(request.url());
        if (url.hostname !== 'auth.llngtest.invalid') return;
        if (url.pathname === '/api/get-account' || url.pathname === '/api/logout') privateCalls++;
        if (url.searchParams.has('post_logout_redirect_uri')) statePresent = Boolean(url.searchParams.get('state'));
      });
      if (origin === 'unavailable-provider') await affected.context.route('https://auth.llngtest.invalid:18443/oauth2/logout**',
        route => route.fulfill({status: 503, contentType: 'text/html', body: 'Provider unavailable'}));
      if (origin === 'provider') await affected.page.goto('https://auth.llngtest.invalid:18443/?logout=1');
      else {
        await affected.page.locator('#logout').click();
        if (origin === 'unavailable-provider') {
          await expect(affected.page.getByText('Provider unavailable')).toBeVisible();
          await affected.page.goto(portal + '/signed-out');
          await expect(affected.page.locator('#iam-retry')).toBeVisible();
        } else {
          await expect(affected.page.locator('body[data-page=signed-out]')).toHaveCount(1);
          await expect(affected.page.locator('.logout-results .step').first()).toHaveClass(/done/);
        }
        expect(statePresent).toBe(true);
      }
      const status = async value => (await value.context.request.get('https://127.0.0.1:18443/api/me',
        {headers: {Host: 'portal.llngtest.invalid:18443', Cookie: value.cookie}})).status();
      expect(await status(affected)).toBe(401); expect(await status(peer)).toBe(200);
      expect(privateCalls).toBe(0);
      if (origin !== 'unavailable-provider') {
        await affected.context.addCookies(affected.iamCookies);
        await affected.page.goto('https://auth.llngtest.invalid:18443/?lmAuth=1_Password');
        await expect(affected.page.locator('input[name=user]').first()).toBeVisible();
      }
      console.log('Portal ' + origin + ' logout: old cookie invalid, unaffected peer retained, provider-private API unused');
    } finally {for (const context of contexts) await context.close();}
  });
}

test('failed background logout survives consumer restart and retries without cross-user logout', async ({browser}) => {
  const contexts = [];
  async function login(name) {
    const context = await browser.newContext({ignoreHTTPSErrors: true}); contexts.push(context);
    await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
    const page = await context.newPage();
    await page.goto(portal + '/login'); await password(page, credentials[name]);
    expect((await api(page, '/api/me')).status).toBe(200);
    const cookie = (await context.cookies(portal)).map(c => c.name + '=' + c.value).join('; ');
    await page.goto('about:blank');
    return {context, cookie};
  }
  directory('enable', 'portalnormal');
  try {
    await expect.poll(() => eventStatus().consumer.pending, {timeout: 60_000}).toBe(0);
    const affected = await login('portalnormal');
    const other = await login('portalother');
    eventFault('block_callback');
    await expect.poll(async () => {
      try {
        return (await affected.context.request.post('https://127.0.0.1:18443/oidc/backchannel-logout',
          {headers: {Host: 'portal.llngtest.invalid:18443'}, data: ''})).status();
      } catch (_) { return 0; } // The test proxy is restarting to install the fault.
    }, {timeout: 20_000}).toBe(502);
    const before = Number(eventStatus().cursor.cursor);
    directory('disable', 'portalnormal');
    await expect.poll(() => eventStatus().consumer.pending, {timeout: 60_000, intervals: [2000]}).toBeGreaterThan(0);
    expect(Number(eventStatus().cursor.cursor)).toBe(before);
    directory('enable', 'portalnormal');
    eventFault('restart_consumer');
    const restarted = eventStatus();
    expect(restarted.consumer.pending).toBeGreaterThan(0);
    expect(Number(restarted.cursor.cursor)).toBe(before);
    eventFault('restore_callback');
    await expect.poll(() => {
      const s = eventStatus();
      return s.consumer.pending === 0 && Number(s.cursor.cursor) >= s.producer.last_published_seq;
    }, {timeout: 60_000, intervals: [2000]}).toBe(true);
    const status = async value => (await value.context.request.get('https://127.0.0.1:18443/api/me',
      {headers: {Host: 'portal.llngtest.invalid:18443', Cookie: value.cookie}})).status();
    expect(await status(affected)).toBe(401);
    expect(await status(other)).toBe(200);
    console.log('Failed callback retained its cursor and durable notification across restart; retry revoked only the affected old cookie');
  } finally {
    eventFault('restore_callback'); directory('enable', 'portalnormal');
    for (const context of contexts) await context.close();
  }
});

test('native Samba event revokes old real portal cookie and preserves another user', async ({browser}) => {
  await expect.poll(() => {
    const s = eventStatus();
    return s.configured && s.consumer.running && s.producer.ready;
  }, {timeout: 60_000, intervals: [3000]}).toBe(true);
  const contexts = [];
  async function login(name) {
    const context = await browser.newContext({ignoreHTTPSErrors: true}); contexts.push(context);
    await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
    const page = await context.newPage();
    await page.goto(portal + '/login'); await password(page, credentials[name]);
    expect((await api(page, '/api/me')).status).toBe(200);
    const cookie = (await context.cookies(portal)).map(c => c.name + '=' + c.value).join('; ');
    // Stop all browser requests while AD is disabled. The later rejection must
    // come from the event callback, not a request-time account check deleting it.
    await page.goto('about:blank');
    return {context, cookie};
  }
  directory('enable', 'portalnormal');
  try {
    const affected = await login('portalnormal');
    const other = await login('portalother');
    const before = eventStatus().producer.last_published_seq || 0;
    const start = Date.now();
    directory('disable', 'portalnormal');
    await expect.poll(() => {
      const s = eventStatus();
      return s.producer.last_published_seq > before && s.consumer.pending === 0
        && s.cursor.cursor >= s.producer.last_published_seq;
    }, {timeout: 60_000, intervals: [1000, 2000, 3000]}).toBe(true);
    console.log('Native event and acknowledged application logout completed in ' + (Date.now() - start) + ' ms');
    directory('enable', 'portalnormal');
    async function oldCookieStatus(value) {
      return (await value.context.request.get('https://127.0.0.1:18443/api/me',
        {headers: {Host: 'portal.llngtest.invalid:18443', Cookie: value.cookie}})).status();
    }
    expect(await oldCookieStatus(affected)).toBe(401);
    expect(await oldCookieStatus(other)).toBe(200);
    console.log('Old application cookie stays revoked after AD re-enable; unaffected user stays authenticated');
  } finally {
    directory('enable', 'portalnormal');
    for (const context of contexts) await context.close();
  }
});

test('accepted Vikunja limitation retains its application session while central grants and portal session are revoked', async ({page, context}) => {
  const auth = 'https://auth.llngtest.invalid:18443';
  directory('enable', 'portalnormal');
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  try {
    await page.goto(portal + '/login'); await password(page, credentials.portalnormal);
    const cookie = (await context.cookies(portal)).map(c => c.name + '=' + c.value).join('; ');
    const iamCookies = await context.cookies(auth);
    await page.goto('https://tasks.llngtest.invalid:18443');
    await expect.poll(async () => {
      if (new URL(page.url()).hostname !== 'tasks.llngtest.invalid') return 0;
      return page.evaluate(async () => {
        const token = localStorage.getItem('token');
        return token ? (await fetch('/api/v1/user', {headers: {Authorization: 'Bearer ' + token}})).status : 0;
      }).catch(() => 0);
    }, {timeout: 30_000}).toBe(200);
    const token = await page.evaluate(() => localStorage.getItem('token'));
    await page.goto('about:blank');
    await expect.poll(() => eventStatus().consumer.pending, {timeout: 60_000}).toBe(0);
    const before = Number(eventStatus().producer.last_published_seq);
    directory('disable', 'portalnormal');
    await expect.poll(() => {
      const s = eventStatus();
      return s.producer.last_published_seq > before && s.consumer.pending === 0
        && Number(s.cursor.cursor) >= s.producer.last_published_seq
        && s.consumer.limited_clients.includes('vikunja-test');
    }, {timeout: 60_000, intervals: [2000]}).toBe(true);
    eventFault('restart_consumer');
    await expect.poll(() => eventStatus().consumer.running, {timeout: 20_000}).toBe(true);
    expect(eventStatus().consumer.limited_clients).toContain('vikunja-test');
    directory('enable', 'portalnormal');
    expect((await context.request.get('https://127.0.0.1:18443/api/me',
      {headers: {Host: 'portal.llngtest.invalid:18443', Cookie: cookie}})).status()).toBe(401);
    expect((await context.request.get('https://127.0.0.1:18443/api/v1/user',
      {headers: {Host: 'tasks.llngtest.invalid:18443', Authorization: 'Bearer ' + token}})).status()).toBe(200);
    await context.addCookies(iamCookies);
    await page.goto(auth + '/?lmAuth=1_Password');
    await expect(page.locator('input[name=user]').first()).toBeVisible();
    console.log('Accepted limitation persists after restart: Vikunja old session remains valid; old portal and LLNG cookies are revoked');
  } finally {directory('enable', 'portalnormal');}
});

test('portal self profile and password writes preserve CSRF and identity boundaries', async ({page, context}) => {
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  directory('restore_password', 'portalnormal');
  try {
    await page.goto(portal + '/login'); await password(page, credentials.portalnormal);
    const me = (await api(page, '/api/me')).data;
    const profile = {given_name: 'LLNG', surname: 'Acceptance', display_name: me.person.display_name};
    const rejected = await page.evaluate(async body => (await fetch('/api/me/profile', {
      method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body),
    })).status, {profile, profile_version: me.profile_version});
    expect(rejected).toBe(403);
    const saved = await api(page, '/api/me/profile', {profile, profile_version: me.profile_version});
    expect(saved.status).toBe(200); expect(saved.data.directory).toBe('saved');
    const fresh = await api(page, '/api/me');
    expect(fresh.data.person.given_name).toBe('LLNG');
    const conflict = await api(page, '/api/me/profile', {profile, profile_version: me.profile_version});
    expect(conflict.status).toBe(409);
    const next = 'Anas9!' + randomUUID();
    const wrong = await api(page, '/api/password/change', {old_password: credentials.portalnormal.password + '-wrong', new_password: next, confirm_password: next});
    expect(wrong.status).toBe(409);
    const changed = await api(page, '/api/password/change', {old_password: credentials.portalnormal.password, new_password: next, confirm_password: next});
    expect(changed.status).toBe(200); expect(changed.data.directory).toBe('saved');
    expect(changed.data.reauthenticate).toBe(true);
    expect((await api(page, '/api/me')).status).toBe(401);
    await context.clearCookies();
    await page.goto(portal + '/login');
    await password(page, {username: 'portalnormal', password: next});
    expect((await api(page, '/api/me')).data.person.anchor).toBe(me.person.anchor);
    console.log('Actual self profile/password writes, stale profile conflict, CSRF rejection and new-password login passed');
  } finally {
    directory('restore_password', 'portalnormal'); directory('restore_profile', 'portalnormal');
  }
});

for (const nested of [false, true]) {
  test(`directory ${nested ? 'nested' : 'direct'} portal group removal preserves the same user's Vikunja session`, async ({page, context}) => {
    await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
    directory('add_portal', 'portalnormal');
    if (nested) { directory('add_nested', 'portalnormal'); directory('nest_portal', 'portalnormal'); }
    try {
      await page.goto(portal + '/login'); await password(page, credentials.portalnormal);
      expect((await api(page, '/api/me')).status).toBe(200);
      const cookie = (await context.cookies(portal)).map(c => c.name + '=' + c.value).join('; ');
      await page.goto('https://tasks.llngtest.invalid:18443');
      await expect.poll(async () => {
        if (new URL(page.url()).hostname !== 'tasks.llngtest.invalid') return 0;
        return page.evaluate(async () => {
          const token = localStorage.getItem('token');
          if (!token) return 0;
          return (await fetch('/api/v1/user', {headers: {Authorization: 'Bearer ' + token}})).status;
        }).catch(() => 0);
      }, {timeout: 30_000}).toBe(200);
      const token = await page.evaluate(() => localStorage.getItem('token'));
      await page.goto('about:blank');
      await expect.poll(() => eventStatus().consumer.pending, {timeout: 60_000}).toBe(0);
      const before = Number(eventStatus().producer.last_published_seq);
      directory(nested ? 'remove_nested' : 'remove_portal', 'portalnormal');
      await expect.poll(() => {
        const s = eventStatus();
        return s.producer.last_published_seq > before && s.consumer.pending === 0 && Number(s.cursor.cursor) >= s.producer.last_published_seq;
      }, {timeout: 60_000, intervals: [2000]}).toBe(true);
      directory(nested ? 'add_nested' : 'add_portal', 'portalnormal');
      expect((await context.request.get('https://127.0.0.1:18443/api/me',
        {headers: {Host: 'portal.llngtest.invalid:18443', Cookie: cookie}})).status()).toBe(401);
      expect((await context.request.get('https://127.0.0.1:18443/api/v1/user',
        {headers: {Host: 'tasks.llngtest.invalid:18443', Authorization: 'Bearer ' + token}})).status()).toBe(200);
      console.log('Affected portal old cookie revoked; same user in unaffected real Vikunja remains authenticated');
    } finally {
      if (nested) { directory('add_nested', 'portalnormal'); directory('unnest_portal', 'portalnormal'); }
      else directory('add_portal', 'portalnormal');
    }
  });
}

test('actual portal delegated reset, unlock and group membership stay within the assigned scope', async ({page, context, browser}) => {
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  directory('restore_password', 'portaltarget'); directory('unlocked', 'portaltarget'); directory('add_a', 'portaltarget');
  let targetContext;
  async function ensureRecent(force = false) {
    if (!force && (await api(page, '/api/me')).data.recent_authentication) return;
    await page.evaluate(async () => {
      const b = await fetch('/api/bootstrap').then(r => r.json());
      const form = document.createElement('form'); form.method = 'POST'; form.action = '/api/reauth';
      const csrf = document.createElement('input'); csrf.name = 'csrf'; csrf.value = b.csrf;
      form.append(csrf); document.body.append(form); form.submit();
    });
    await password(page, credentials.portalmanager);
  }
  try {
    await page.goto(portal + '/login'); await password(page, credentials.portalmanager);
    const me = (await api(page, '/api/me')).data;
    const users = await api(page, '/api/users');
    expect(users.status).toBe(200);
    expect(users.data.users.map(p => p.username)).toEqual(['portaltarget']);
    const target = users.data.users[0].anchor;
    expect((await api(page, '/api/users/' + me.person.anchor)).status).toBe(403);
    expect((await api(page, '/api/users/' + me.person.anchor + '/reset', {force_change: false})).status).toBe(403);
    const groups = (await api(page, '/api/groups?target=' + target)).data.groups;
    expect(groups).toHaveLength(1); expect(groups[0].group.name).toBe('LLNG App A');
    const group = groups[0].group.anchor;
    await ensureRecent();
    const removed = await api(page, '/api/groups/' + group + '/member', {target, add: false, expected_direct: true});
    expect(removed.status).toBe(200); expect(removed.data.directory).toBe('saved');
    expect((await api(page, '/api/groups?target=' + target)).data.groups[0].direct).toBe(false);
    expect((await api(page, '/api/groups/' + group + '/member', {target, add: true, expected_direct: true})).status).toBe(409);
    await ensureRecent();
    expect((await api(page, '/api/groups/' + group + '/member', {target, add: true, expected_direct: false})).status).toBe(200);
    directory('locked', 'portaltarget');
    expect((await api(page, '/api/users/' + target)).data.person.locked).toBe(true);
    await ensureRecent();
    expect((await api(page, '/api/users/' + target + '/unlock', {})).status).toBe(200);
    expect((await api(page, '/api/users/' + target)).data.person.locked).toBe(false);
    await ensureRecent(true);
    const reset = await api(page, '/api/users/' + target + '/reset', {force_change: false});
    expect(reset.status, reset.data.error || 'delegated reset status').toBe(200); expect(reset.data.directory).toBe('saved');
    expect(typeof reset.data.temporary_password).toBe('string');
    targetContext = await browser.newContext({ignoreHTTPSErrors: true});
    await targetContext.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
    const targetPage = await targetContext.newPage(); await targetPage.goto(portal + '/login');
    await password(targetPage, {username: 'portaltarget', password: reset.data.temporary_password});
    expect((await api(targetPage, '/api/me')).data.person.anchor).toBe(target);
    const audit = await api(page, '/api/audit'); expect(audit.status).toBe(200);
    expect(audit.data.records.some(r => r.target === target && r.action === 'password.reset')).toBe(true);
    console.log('Delegated reset/new-password login, unlock, scoped group writes, stale membership and out-of-scope denial passed');
  } finally {
    await targetContext?.close();
    directory('restore_password', 'portaltarget'); directory('unlocked', 'portaltarget'); directory('add_a', 'portaltarget');
  }
});

for (const fault of ['partial_journal', 'rotated_duplicate', 'retained_gap', 'corrupt_cursor']) {
  test(`directory journal ${fault} recovers and revokes only the affected real application cookie`, async ({browser}) => {
    const contexts = [];
    let journalFault = false;
    async function login(name) {
      const context = await browser.newContext({ignoreHTTPSErrors: true}); contexts.push(context);
      await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
      const page = await context.newPage(); await page.goto(portal + '/login');
      await password(page, credentials[name]);
      expect((await api(page, '/api/me')).status).toBe(200);
      const cookie = (await context.cookies(portal)).map(c => c.name + '=' + c.value).join('; ');
      await page.goto('about:blank');
      return {context, cookie};
    }
    directory('enable', 'portalother');
    try {
      await expect.poll(() => {
        const s = eventStatus();
        return s.consumer.pending === 0 && Number(s.cursor.cursor) >= s.producer.last_published_seq;
      }, {timeout: 60_000, intervals: [2000]}).toBe(true);
      const affected = await login('portalother');
      const other = await login('portalmanager');
      eventFault('stop_consumer');
      const before = Number(eventStatus().cursor.cursor);
      directory('disable', 'portalother');
      if (fault === 'retained_gap') {
        directory('enable', 'portalother'); directory('disable', 'portalother');
      }
      await expect.poll(() => Number(eventStatus().producer.last_published_seq),
        {timeout: 40_000, intervals: [2000]}).toBeGreaterThan(before + (fault === 'retained_gap' ? 1 : 0));
      journalFault = true;
      eventFault(fault);
      if (fault === 'partial_journal') {
        // Observe multiple actual consumer polls while the final line has no LF.
        const start = Date.now();
        await expect.poll(() => {
          expect(Number(eventStatus().cursor.cursor)).toBe(before);
          return Date.now() - start;
        }, {timeout: 15_000, intervals: [2000]}).toBeGreaterThan(6000);
        eventFault('complete_journal');
      }
      await expect.poll(() => {
        const s = eventStatus();
        return s.consumer.running && s.consumer.pending === 0 && Number(s.cursor.cursor) >= s.producer.last_published_seq;
      }, {timeout: 60_000, intervals: [2000]}).toBe(true);
      eventFault('restore_journal'); journalFault = false;
      directory('enable', 'portalother');
      const status = async value => (await value.context.request.get('https://127.0.0.1:18443/api/me',
        {headers: {Host: 'portal.llngtest.invalid:18443', Cookie: value.cookie}})).status();
      expect(await status(affected)).toBe(401);
      expect(await status(other)).toBe(200);
      console.log('Real journal fault ' + fault + ' recovered; old affected cookie rejected and unaffected cookie retained');
    } finally {
      if (journalFault) eventFault('restore_journal');
      eventFault('start_consumer');
      directory('enable', 'portalother');
      for (const context of contexts) await context.close();
    }
  });
}
