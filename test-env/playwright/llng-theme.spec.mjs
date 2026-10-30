// TEST_CASES: LLNG-READY-T-004
import { mkdirSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const auth = 'https://auth.llngtest.invalid:18443';
for (const locale of ['en-US', 'zh-CN']) {
  for (const viewport of [{width: 1280, height: 900}, {width: 390, height: 844}]) {
    test(`native theme ${locale} ${viewport.width}: preferences, language and authentication controls`, async ({browser}) => {
      const context = await browser.newContext({locale, viewport, colorScheme: 'light', ignoreHTTPSErrors: true});
      await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
      const page = await context.newPage();
      const errors = []; page.on('pageerror', error => errors.push(error.name));
      try {
        await page.goto(auth + '/?lmAuth=1_Password&theme=dark');
        await expect(page.locator('html')).toHaveAttribute('data-anas-theme', 'dark');
        await expect(page.locator('input[name=user]').first()).toBeVisible();
        await expect(page.locator('input[name=password]').first()).toBeVisible();
        await expect.poll(() => page.locator('input[name=user]').first().getAttribute('placeholder')).toBeTruthy();
        expect(await page.evaluate(() => window.currentLanguage)).toBe(locale === 'zh-CN' ? 'zh' : 'en');
        await expect(page.locator('.anas-theme-toggle')).toContainText(locale === 'zh-CN' ? '外观' : 'Appearance');
        expect(await page.locator('#header img').first().evaluate(img => img.complete && img.naturalWidth > 0)).toBe(true);
        expect(await page.evaluate(() => getComputedStyle(document.body).backgroundColor)).toBe('rgb(15, 17, 21)');
        expect(await page.locator('#authMenu .ui-tabs-active').evaluate(el => getComputedStyle(el).backgroundColor)).toBe('rgb(37, 39, 74)');
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
        const dir = process.env.ANAS_TEST_SCREENSHOT_DIR;
        if (dir) {
          mkdirSync(dir, {recursive: true});
          // Signed-out screenshots only. Never capture credentials or sessions.
          await page.screenshot({path: `${dir}/${locale}-${viewport.width}-dark.png`, fullPage: true});
        }
        await page.locator('.anas-theme-toggle').click(); // dark -> system
        await expect(page.locator('html')).toHaveAttribute('data-anas-theme', 'light');
        await page.emulateMedia({colorScheme: 'dark'});
        await expect(page.locator('html')).toHaveAttribute('data-anas-theme', 'dark');
        await page.goto(auth + '/?lmAuth=2_Passkey');
        await expect(page.locator('#webauthnbutton')).toBeVisible();
        await expect(page.locator('html')).toHaveAttribute('data-anas-theme', 'dark');
        expect(errors).toHaveLength(0);
      } finally { await context.close(); }
    });
  }
}
