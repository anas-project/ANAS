// TEST_CASES: LLNG-READY-T-001
import {after, before, test} from 'node:test';
import {readFile} from 'node:fs/promises';
import {chromium, expect} from '@playwright/test';

let browser;
before(async () => {browser = await chromium.launch({headless: true});});
after(async () => {await browser?.close();});
const portal = 'https://portal.example.test', iam = 'https://auth.example.test';
const source = new URL('../../modules/user_portal/portal/ui/', import.meta.url);

for (const outcome of ['confirmed', 'failed', 'unavailable']) {
  test(`standard OIDC logout shows ${outcome} without calling a provider-private API`, async () => {
    const context = await browser.newContext(), page = await context.newPage();
    let privateCalls = 0, navigations = 0;
    await context.route('**/*', async route => {
      const request = route.request(), url = new URL(request.url()), path = url.pathname;
      const json = data => route.fulfill({contentType: 'application/json', body: JSON.stringify(data)});
      if (path.startsWith('/static/')) return route.fulfill({contentType: path.endsWith('.js') ? 'text/javascript' : 'text/css',
        body: await readFile(new URL(path.slice(8), source), 'utf8')});
      if (path === '/api/bootstrap') return json({authenticated: false, csrf: 'fixture-csrf', language: 'en'});
      if (path === '/api/logout/complete') {
        expect(request.headers()['x-csrf-token']).toBe('fixture-csrf');
        return json({iam_endpoint: iam + '/oauth2/logout', local: 'logged_out', targets: [],
          iam_redirect: iam + '/oauth2/logout?client_id=portal&state=fixture&post_logout_redirect_uri=' + encodeURIComponent(portal + '/oidc/logout-callback')});
      }
      if (url.origin === iam && path === '/oauth2/logout') {
        expect(request.method()).toBe('GET'); expect(url.searchParams.get('state')).toBe('fixture'); navigations++;
        if (outcome === 'unavailable') return route.fulfill({status: 503, contentType: 'text/html', body: 'Provider unavailable'});
        // A fresh navigation keeps the mock callback inside Playwright routing;
        // redirected requests can bypass its interception after a fulfilled 302.
        return route.fulfill({contentType: 'text/html', body: '<meta http-equiv="refresh" content="0;url=' + portal + '/oidc/logout-callback?state=fixture">'});
      }
      if (path === '/signed-out' || path === '/oidc/logout-callback') {
        let html = await readFile(new URL('index.html', source), 'utf8');
        if (path === '/oidc/logout-callback') html = html.replace('<head>', '<head><meta name="anas-oidc-logout" content="' + outcome + '">');
        else if (url.searchParams.has('native')) html = html.replace('<head>', '<head><meta name="anas-native-logout" content="automatic">');
        return route.fulfill({contentType: 'text/html', body: html});
      }
      privateCalls++; return route.abort();
    });
    try {
      await page.goto(portal + '/signed-out?native=1');
      await expect.poll(() => navigations).toBe(1);
      if (outcome === 'unavailable') {
        await expect(page.getByText('Provider unavailable')).toBeVisible();
        await page.goto(portal + '/signed-out');
      }
      await expect(page.locator('body[data-page=signed-out]')).toHaveCount(1);
      if (outcome === 'confirmed') await expect(page.getByText('The sign-outs listed above are confirmed.', {exact: true})).toBeVisible();
      else await expect(page.getByRole('button', {name: 'Retry sign-out', exact: true})).toBeVisible();
      expect(privateCalls).toBe(0);
      expect(new URL(page.url()).pathname).toBe('/signed-out');
    } finally {await context.close();}
  });
}
