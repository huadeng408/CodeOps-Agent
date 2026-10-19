// Local static UI only; this is not a deployed Cloudflare or Go/provider receipt.
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { chromium } from 'playwright';

const output = resolve(process.env.CODEOPS_STATIC_PREVIEW_EVIDENCE || '../output/playwright/static-preview-' + Date.now());
await mkdir(output, { recursive: true });
const base = process.env.CODEOPS_STATIC_PREVIEW_URL || 'http://127.0.0.1:4173';
let browser, page;
const calls = [], sockets = [], errors = [];
const teardownErrors = [];
let browserClose = 'not_created';
const checks = Object.fromEntries(['home', 'notice', 'login-disabled', 'register-disabled', 'submit-inert', 'documentation', 'deep-link', 'refresh', 'assets', 'no-api-or-socket', 'no-page-errors'].map(name => [name, 'NOT RUN']));
async function check(name, action) {
  checks[name] = 'FAIL';
  await action();
  checks[name] = 'PASS';
}
try {
  browser = await chromium.launch({ headless: true });
  browserClose = 'unknown';
  page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  page.on('request', request => { if (/\/api\/|\/healthz(?:\?|$)/.test(new URL(request.url()).pathname)) calls.push(request.url()); });
  page.on('websocket', socket => sockets.push(socket.url()));
  page.on('pageerror', error => errors.push(error.message));
  await page.route('**/api/**', route => route.abort());
  await page.route('**/healthz', route => route.abort());
  await check('home', async () => assert.equal((await page.goto(base)).status(), 200));
  await check('notice', async () => {
    await page.getByRole('status').filter({ hasText: '静态界面预览' }).waitFor({ timeout: 2000 });
  });
  await check('login-disabled', async () => {
    assert.ok(await page.getByPlaceholder('you@example.com').isDisabled());
    assert.ok(await page.getByPlaceholder('••••••••').isDisabled());
    assert.ok(await page.locator('button[type=submit]').isDisabled());
  });
  await check('register-disabled', async () => {
    await page.getByRole('button', { name: 'Register', exact: true }).first().click();
    assert.ok(await page.getByPlaceholder('Your name').isDisabled());
    assert.ok(await page.locator('button[type=submit]').isDisabled());
  });
  await check('submit-inert', async () => {
    await page.locator('form').evaluate(form => form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
    assert.equal(calls.length, 0);
  });
  await check('documentation', async () => {
    assert.equal(await page.getByRole('link', { name: '项目与使用说明' }).getAttribute('href'), 'https://github.com/huadeng408/CodeOps-Agent#readme');
  });
  await check('deep-link', async () => {
    assert.equal((await page.goto(base + '/preview/task-workspace')).status(), 200);
    await page.getByRole('status').filter({ hasText: '静态界面预览' }).waitFor();
  });
  await check('refresh', async () => {
    assert.equal((await page.reload()).status(), 200);
    await page.getByRole('status').filter({ hasText: '静态界面预览' }).waitFor();
  });
  await check('assets', async () => {
    assert.ok(await page.locator('script[type=module]').count());
    assert.ok(await page.locator('link[rel=stylesheet]').count());
    assert.equal(await page.locator('.login-container').evaluate(node => getComputedStyle(node).display), 'flex');
  });
  await check('no-api-or-socket', async () => { assert.deepEqual(calls, []); assert.deepEqual(sockets, []); });
  await check('no-page-errors', async () => assert.deepEqual(errors, []));
} catch (error) {
  process.exitCode = 1;
  await writeFile(resolve(output, 'failure.txt'), String(error));
} finally {
  if (page) {
    try { await page.screenshot({ path: resolve(output, 'preview.png') }); }
    catch (error) { teardownErrors.push(String(error)); process.exitCode = 1; }
  }
  if (browser) {
    try { await browser.close(); browserClose = 'closed'; }
    catch (error) { teardownErrors.push(String(error)); process.exitCode = 1; }
  }
  const receipt = { scope: 'Local Vite static-preview UI, guarded network, no cloud/account/Go/provider validation', checks, calls, sockets, errors, teardownErrors, browser_close: browserClose, exit_code: process.exitCode || 0 };
  await writeFile(resolve(output, 'receipt.json'), JSON.stringify(receipt, null, 2));
  console.log(JSON.stringify(receipt));
}
