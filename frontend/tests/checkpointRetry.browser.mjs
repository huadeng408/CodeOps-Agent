// Public UI regression with external HTTP/WebSocket fixtures; no provider evidence.
import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const browser = await chromium.launch({ headless: true });
const page = await browser.newPage();
const output = resolve('../output/playwright/checkpoint-retry-' + Date.now());
await mkdir(output, { recursive: true });
const run = { sessionId: 'retry-fixture', runId: 'failed-run', requestId: 'original-request', status: 'failed', attempt: 1, checkpointHash: 'c'.repeat(64) };
const session = { id: run.sessionId, userId: 7, projectName: 'fixture', title: 'Retry automatic anchor', workingDir: '.', goal: 'Retry the original input', status: 'paused', eventCount: 7, run, createdAt: '2026-10-14T00:00:00Z', updatedAt: '2026-10-14T00:00:00Z' };
const attempts = [], errors = [];
page.on('pageerror', error => errors.push(error.message));
let failed = true;
try {
  await page.route('**/healthz', route => route.fulfill({ json: { continuation: { attached: true, consecutive_failures: 0 } } }));
  await page.routeWebSocket(/\/api\/v1\/sessions\/.+\/ws\?/, () => {});
  await page.route('**/api/v1/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname;
    let data = [];
    if (path.endsWith('/users/me')) data = { id: 7, username: 'fixture' };
    else if (path === '/api/v1/sessions') data = [session];
    else if (path === '/api/v1/sessions/' + session.id) data = session;
    else if (path.endsWith('/ws-ticket')) data = { ticket: 'fixture' };
    else if (path.endsWith('/runs')) data = [run];
    else if (path.endsWith('/task-workspace')) data = { available: false, state: 'unprepared' };
    else if (path.endsWith('/continue')) {
      attempts.push(request.postDataJSON());
      session.status = 'done';
      data = { ...run, runId: 'retry-run', status: 'queued', requestId: attempts[0].requestId };
    }
    await route.fulfill({ json: { data } });
  });
  await page.goto(process.argv[2] || 'http://127.0.0.1:3000/');
  const retry = page.getByRole('button', { name: '再次运行', exact: true });
  await retry.waitFor();
  const response = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/continue'), { timeout: 3000 });
  await retry.click();
  await response;
  assert.equal(attempts.length, 1);
  assert.equal(attempts[0].checkpointHash, run.checkpointHash);
  assert.equal(attempts[0].expectedSeq, 7);
  assert.ok(attempts[0].requestId);
  assert.deepEqual(errors, []);
  failed = false;
  console.log('PASS: failed automatic-checkpoint run retries without a manual checkpoint');
} finally {
  await page.screenshot({ path: resolve(output, failed ? 'failed.png' : 'passed.png') });
  await writeFile(resolve(output, 'result.json'), JSON.stringify({ evidence_type: 'fixture-browser-ui', provider_backed: false, exit_code: failed ? 1 : 0, attempts, page_errors: errors }, null, 2));
  await browser.close();
  console.log('Browser artifacts: ' + output);
}
