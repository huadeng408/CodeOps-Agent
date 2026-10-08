// UI regression with fixture HTTP/WebSocket responses; no Harness or provider evidence.
// Start npm run dev, then: node tests/sendMessage.browser.mjs [--headed]
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { execFileSync } from 'node:child_process';
import { chromium } from 'playwright';

const browser = await chromium.launch({ headless: !process.argv.includes('--headed') });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const output = resolve('../output/playwright/send-message-' + Date.now());
await mkdir(output, { recursive: true });
const session = {
  id: 'cursor-fixture', userId: 7, title: 'Cursor regression fixture',
  projectName: 'CodeOps-Agent', goal: 'Verify browser sends', workingDir: '.',
  status: 'done', eventCount: 0,
  createdAt: '2026-10-08T00:00:00Z', updatedAt: '2026-10-08T00:00:00Z',
};
const events = [], attempts = [], sockets = new Set();
const pageErrors = [];
page.on('pageerror', (error) => pageErrors.push(error.message));
const checks = { ten_turns: false, reload: false, concurrent_retry: false, unavailable_draft: false, no_page_errors: false };
let race = false, unavailable = false, accepted = 0, failed = true;
const addEvent = (type, author, content) => events.push({
  id: 'event-' + session.eventCount, seq: session.eventCount++,
  sessionId: session.id, type, author, content,
  hash: 'a'.repeat(64), prevHash: 'b'.repeat(64), createdAt: session.updatedAt,
});
try {
  await page.route('**/healthz', (route) => route.fulfill({
    json: { status: 'ok', continuation: { attached: true, consecutive_failures: 0 } },
  }));
  await page.routeWebSocket(/\/api\/v1\/sessions\/.+\/ws\?/, (socket) => {
    sockets.add(socket);
    socket.onClose(() => sockets.delete(socket));
  });
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request(), url = new URL(request.url());
    let data = [];
    if (url.pathname.endsWith('/users/me')) data = { id: 7, username: 'fixture' };
    else if (url.pathname === '/api/v1/sessions') data = [session];
    else if (url.pathname.endsWith('/ws-ticket')) data = { ticket: 'fixture', expiresAt: session.updatedAt };
    else if (url.pathname.endsWith('/events')) data = events.filter((event) => event.seq > Number(url.searchParams.get('after') ?? -1));
    else if (url.pathname.endsWith('/messages')) {
      const body = request.postDataJSON();
      attempts.push(body);
      if (unavailable) return route.fulfill({ status: 503, json: { message: 'fixture unavailable' } });
      if (race) { race = false; addEvent('memory/catalog-updated', 'system', 'fixture concurrent event'); }
      if (body.expectedSeq !== session.eventCount) return route.fulfill({ status: 409, json: { message: 'fixture sequence conflict' } });
      accepted++;
      addEvent('user/message', 'user', body.content);
      addEvent('assistant/message', 'assistant', 'Fixture reply ' + accepted);
      for (const socket of sockets) for (const event of events.slice(-2)) socket.send(JSON.stringify(event));
      data = { sessionId: session.id, runId: 'run-' + accepted, requestId: body.requestId, status: 'completed', attempt: 1, checkpointHash: '' };
    } else if (url.pathname === '/api/v1/sessions/' + session.id) data = session;
    await route.fulfill({ json: { data } });
  });
  await page.goto('http://127.0.0.1:3000/');
  const composer = page.locator('textarea.input-box');
  await composer.waitFor();
  for (let turn = 1; turn <= 10; turn++) {
    // The UI has finished polling; a background fact advances the canonical head.
    addEvent('memory/catalog-updated', 'system', 'fixture late memory event');
    await composer.fill('Browser fixture turn ' + turn);
    await page.getByRole('button', { name: '发送', exact: true }).click();
    await page.getByText('Fixture reply ' + turn, { exact: true }).waitFor();
    await page.waitForFunction(() => document.querySelector('textarea.input-box')?.value === '');
    assert.equal(attempts.length, turn, 'initial send used a stale cursor and required a 409 retry');
  }
  checks.ten_turns = true;
  await page.reload();
  await page.getByText('Fixture reply 1', { exact: true }).waitFor();
  await page.getByText('Fixture reply 10', { exact: true }).waitFor();
  checks.reload = true;
  race = true;
  await composer.fill('Concurrent fixture turn');
  await page.getByRole('button', { name: '发送', exact: true }).click();
  await page.getByText('Fixture reply 11', { exact: true }).waitFor();
  await page.waitForFunction(() => document.querySelector('textarea.input-box')?.value === '');
  assert.equal(attempts.length, 12);
  assert.equal(attempts[10].requestId, attempts[11].requestId);
  assert.equal(attempts[10].content, attempts[11].content);
  checks.concurrent_retry = true;
  unavailable = true;
  await composer.fill('Preserved fixture draft');
  await page.getByRole('button', { name: '发送', exact: true }).click();
  await page.getByRole('button', { name: '重试发送', exact: true }).waitFor();
  assert.equal(await composer.inputValue(), 'Preserved fixture draft');
  unavailable = false;
  await page.getByRole('button', { name: '重试发送', exact: true }).click();
  await page.getByText('Fixture reply 12', { exact: true }).waitFor();
  assert.equal(attempts.length, 14);
  assert.equal(attempts[12].requestId, attempts[13].requestId);
  assert.equal(accepted, 12);
  checks.unavailable_draft = true;
  assert.deepEqual(pageErrors, []);
  checks.no_page_errors = true;
  await page.screenshot({ path: resolve(output, 'passed.png'), fullPage: true });
  failed = false;
  console.log('PASS: 10 browser fixture turns, reload, bounded 409 retry, 503 draft recovery');
} catch (error) {
  await page.screenshot({ path: resolve(output, 'failed.png'), fullPage: true });
  throw error;
} finally {
  await writeFile(resolve(output, 'result.json'), JSON.stringify({
    evidence_type: 'fixture-browser-ui-regression', provider_backed: false, trace_id: null,
    git_sha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
    source_dirty: Boolean(execFileSync('git', ['status', '--porcelain'], { encoding: 'utf8' }).trim()),
    exit_code: failed ? 1 : 0, turns_planned: 12,
    accepted, attempted: attempts.length, checks,
    passed: Object.values(checks).filter(Boolean).length, total: Object.keys(checks).length,
  }, null, 2));
  await browser.close();
  console.log('Browser artifacts: ' + output);
}
