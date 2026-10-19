// Browser UI integration with HTTP/WebSocket fixtures; no Go/provider evidence.
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { chromium } from 'playwright';

const base = process.argv[2] || 'http://127.0.0.1:4174';
const output = resolve(process.env.CODEOPS_RECONNECT_EVIDENCE || '../output/playwright/websocket-reconnect-' + Date.now());
await mkdir(output, { recursive: true });
const session = {
  id: 'reconnect-fixture', userId: 7, projectName: 'fixture', title: 'Reconnect regression',
  workingDir: '.', goal: '', status: 'done', eventCount: 0,
  createdAt: '2026-10-19T00:00:00Z', updatedAt: '2026-10-19T00:00:00Z',
};
const events = [];
const append = (author, content) => events.push({
  id: 'event-' + session.eventCount, seq: session.eventCount++, sessionId: session.id,
  type: author + '/message', author, content, createdAt: session.updatedAt,
});
append('user', 'Initial request');
append('assistant', 'Initial reply');
const checks = Object.fromEntries(['initial-history', 'other-tab-progress', 'reconnect-history', 'draft-and-no-resend', 'reload-history', 'no-page-errors'].map(name => [name, 'NOT RUN']));
const cursors = [], submissions = [], errors = [], teardownErrors = [];
let browser, disconnectedPage, otherPage;
let browserClose = 'not_created', holdTickets = false;
const disconnectedSockets = new Set();
const allSockets = new Set();
async function check(name, run) {
  checks[name] = 'FAIL';
  await run();
  checks[name] = 'PASS';
}
async function showHistory(page, expected) {
  await page.getByText(expected.at(-1), { exact: true }).waitFor({ timeout: 5000 });
  assert.deepEqual(await page.locator('.message-content').allTextContents(), expected);
}
try {
  browser = await chromium.launch({ headless: true });
  browserClose = 'unknown';
  const context = await browser.newContext({ viewport: { width: 1440, height: 960 } });
  disconnectedPage = await context.newPage();
  otherPage = await context.newPage();
  for (const [page, name] of [[disconnectedPage, 'disconnected'], [otherPage, 'other']]) {
    page.on('pageerror', error => errors.push({ page: name, error: error.message }));
    await page.route('**/healthz', route => route.fulfill({ json: { continuation: { attached: false, consecutive_failures: 0 } } }));
    await page.route('**/api/v1/**', async route => {
      const request = route.request(), url = new URL(request.url());
      let data = [];
      if (url.pathname.endsWith('/users/me')) data = { id: 7, username: 'fixture' };
      else if (url.pathname === '/api/v1/sessions') data = [session];
      else if (url.pathname === '/api/v1/sessions/' + session.id) data = session;
      else if (url.pathname.endsWith('/ws-ticket')) {
        if (name === 'disconnected' && holdTickets) return route.fulfill({ status: 503, json: { message: 'simulated disconnect' } });
        data = { ticket: 'fixture' };
      } else if (url.pathname.endsWith('/events')) {
        data = events.filter(event => event.seq > Number(url.searchParams.get('after') ?? -1));
      } else if (url.pathname.endsWith('/task-workspace')) data = { available: false, state: 'unprepared' };
      else if (url.pathname.endsWith('/messages')) {
        const body = request.postDataJSON();
        submissions.push({ page: name, ...body });
        if (body.expectedSeq !== session.eventCount) return route.fulfill({ status: 409, json: { message: 'fixture event sequence conflict' } });
        append('user', body.content);
        append('assistant', 'Reply received in other tab');
        for (const socket of allSockets) for (const event of events.slice(-2)) socket.send(JSON.stringify(event));
        data = { sessionId: session.id, runId: 'fixture-run', requestId: body.requestId, status: 'completed', attempt: 1 };
      }
      await route.fulfill({ json: { data } });
    });
    await page.routeWebSocket(/\/api\/v1\/sessions\/.+\/ws\?/, socket => {
      allSockets.add(socket);
      if (name === 'disconnected') disconnectedSockets.add(socket);
      const after = Number(new URL(socket.url()).searchParams.get('after'));
      cursors.push({ page: name, after });
      socket.onClose(() => { allSockets.delete(socket); disconnectedSockets.delete(socket); });
      for (const event of events.filter(event => event.seq > after)) socket.send(JSON.stringify(event));
    });
  }
  await check('initial-history', async () => {
    await disconnectedPage.goto(base);
    await showHistory(disconnectedPage, ['Initial request', 'Initial reply']);
    await disconnectedPage.locator('.connection-dot.connected').waitFor();
    await otherPage.goto(base);
    await showHistory(otherPage, ['Initial request', 'Initial reply']);
    await otherPage.locator('.connection-dot.connected').waitFor();
    await disconnectedPage.locator('textarea.input-box').fill('Keep this unsent draft');
  });
  await check('other-tab-progress', async () => {
    holdTickets = true;
    for (const socket of disconnectedSockets) socket.close({ code: 1012, reason: 'test reconnect' });
    await disconnectedPage.locator('.connection-dot.reconnecting').waitFor();
    await otherPage.locator('textarea.input-box').fill('Request from other tab');
    await otherPage.getByRole('button', { name: '发送', exact: true }).click();
    await showHistory(otherPage, ['Initial request', 'Initial reply', 'Request from other tab', 'Reply received in other tab']);
    // Also cover hints left by an older frontend in another tab.
    await otherPage.evaluate(({ id, cursor }) => localStorage.setItem('codeops:ledger-cursor:' + id, String(cursor)), { id: session.id, cursor: 3 });
    assert.equal(await disconnectedPage.getByText('Reply received in other tab', { exact: true }).count(), 0);
  });
  await check('reconnect-history', async () => {
    holdTickets = false;
    await disconnectedPage.locator('.connection-dot.connected').waitFor();
    await showHistory(disconnectedPage, ['Initial request', 'Initial reply', 'Request from other tab', 'Reply received in other tab']);
  });
  await check('draft-and-no-resend', async () => {
    assert.equal(await disconnectedPage.locator('textarea.input-box').inputValue(), 'Keep this unsent draft');
    assert.equal(submissions.length, 1);
    assert.equal(submissions[0].page, 'other');
  });
  await check('reload-history', async () => {
    await disconnectedPage.reload();
    await showHistory(disconnectedPage, ['Initial request', 'Initial reply', 'Request from other tab', 'Reply received in other tab']);
    assert.equal(submissions.length, 1);
  });
  await check('no-page-errors', async () => assert.deepEqual(errors, []));
} catch (error) {
  process.exitCode = 1;
  await writeFile(resolve(output, 'failure.txt'), String(error));
} finally {
  if (disconnectedPage) {
    try { await disconnectedPage.screenshot({ path: resolve(output, 'disconnected-tab.png') }); }
    catch (error) { teardownErrors.push(String(error)); process.exitCode = 1; }
  }
  if (browser) {
    try { await browser.close(); browserClose = 'closed'; }
    catch (error) { teardownErrors.push(String(error)); process.exitCode = 1; }
  }
  const receipt = { scope: 'Browser UI with HTTP/WebSocket fixtures; not Go/provider runtime evidence', checks, cursors, submissions, errors, teardownErrors, browser_close: browserClose, exit_code: process.exitCode || 0 };
  await writeFile(resolve(output, 'receipt.json'), JSON.stringify(receipt, null, 2));
  console.log(JSON.stringify(receipt));
}
