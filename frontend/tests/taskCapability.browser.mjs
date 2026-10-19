// Browser UI integration with external HTTP/WebSocket fixtures; no model calls.
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { chromium } from 'playwright';

const output = resolve(process.env.CODEOPS_CAPABILITY_EVIDENCE || '../output/playwright/task-capability-' + Date.now());
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const session = {
  id: 'capability-fixture', userId: 7, title: 'Capability refresh fixture', projectName: 'CodeOps-Agent',
  goal: 'Finish admitted task', workingDir: '.', status: 'done', eventCount: 1,
  createdAt: '2026-10-20T00:00:00Z', updatedAt: '2026-10-20T00:00:00Z',
};
let held = false, blocked = false, capabilityReads = 0, failed = true;
const submissions = [], errors = [], events = [], finishes = [], sockets = new Set();
const checks = { initial_unowned_hidden: false, admission_refresh: false, background_blocked: false, background_finished: false, unchanged_budget: false, no_automatic_finish: false, no_page_errors: false };
function backgroundEvent(type) {
  const event = { id: 'event-' + session.eventCount, seq: session.eventCount++, sessionId: session.id, type, author: 'system', content: '', createdAt: session.updatedAt };
  events.push(event);
  for (const socket of sockets) socket.send(JSON.stringify(event));
}
page.on('pageerror', error => errors.push(error.message));
try {
  await page.route('**/healthz', route => route.fulfill({ json: { continuation: { attached: true, consecutive_failures: 0 } } }));
  await page.routeWebSocket(/\/api\/v1\/sessions\/.+\/ws\?/, socket => {
    sockets.add(socket);
    socket.onClose(() => sockets.delete(socket));
  });
  await page.route('**/api/v1/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname;
    let data = [];
    if (path.endsWith('/users/me')) data = { id: 7, username: 'fixture' };
    else if (path === '/api/v1/sessions') data = [session];
    else if (path === '/api/v1/sessions/' + session.id) data = session;
    else if (path.endsWith('/events')) data = events.filter(event => event.seq > Number(new URL(request.url()).searchParams.get('after') ?? -1));
    else if (path.endsWith('/ws-ticket')) data = { ticket: 'fixture' };
    else if (path.endsWith('/task-workspace')) data = { available: false, state: 'unprepared', reason: 'fixture' };
    else if (path.endsWith('/capabilities')) {
      capabilityReads++;
      data = { budget: { state: blocked ? 'blocked' : 'ready', reason: blocked ? 'a model attempt is still active; wait or recover its execution' : 'provider enforces budget; local usage is audited',
        tokens: { batch_id: 'original-fixture-batch', task_id: held ? session.id : undefined, limit: 100_000_000, used: 11, reserved: 0, cost_status: 'unknown' } }, execution: { state: 'ready' } };
    } else if (path.endsWith('/messages') && request.method() === 'POST') {
      const body = request.postDataJSON(); submissions.push(body); held = true; session.eventCount = 2;
      session.run = { sessionId: session.id, runId: 'new-fixture-run', requestId: body.requestId, status: 'completed', attempt: 1, checkpointHash: '' };
      return route.fulfill({ status: 202, json: { data: session.run } });
    } else if (path.endsWith('/task/finish')) {
      finishes.push(path);
    }
    await route.fulfill({ json: { data } });
  });
  await page.goto(process.argv[2] || 'http://127.0.0.1:4174');
  await page.locator('textarea.input-box').waitFor();
  await page.getByText('每项能力独立报告状态', { exact: true }).waitFor();
  await page.locator('.connection-dot.connected').waitFor();
  assert.ok(capabilityReads > 0);
  const finish = page.getByRole('button', { name: '结束代码任务', exact: true });
  assert.equal(await finish.count(), 0);
  checks.initial_unowned_hidden = true;
  await page.locator('textarea.input-box').fill('Read the current source file.');
  await page.getByRole('button', { name: '发送', exact: true }).click();
  await finish.waitFor({ state: 'visible', timeout: 3000 });
  assert.equal(await finish.isEnabled(), true);
  assert.equal(submissions.length, 1);
  checks.admission_refresh = true;
  blocked = true;
  backgroundEvent('memory/trajectory-committed');
  await page.getByRole('combobox', { name: '执行记录筛选', exact: true }).selectOption('all');
  await page.getByText('memory/trajectory-committed', { exact: true }).waitFor();
  await page.waitForFunction(() => document.querySelector('button[aria-label="结束代码任务"]')?.disabled === true, undefined, { timeout: 3000 });
  checks.background_blocked = true;
  blocked = false;
  backgroundEvent('memory/reflection-proposed');
  await page.getByText('memory/reflection-proposed', { exact: true }).waitFor();
  await page.waitForFunction(() => document.querySelector('button[aria-label="结束代码任务"]')?.disabled === false, undefined, { timeout: 3000 });
  checks.background_finished = true;
  await page.getByText(/已确认 11 tokens，批准上限 100,000,000/).waitFor();
  checks.unchanged_budget = true;
  assert.deepEqual(finishes, []);
  checks.no_automatic_finish = true;
  assert.deepEqual(errors, []);
  checks.no_page_errors = true;
  await finish.scrollIntoViewIfNeeded();
  failed = false;
} finally {
  await page.screenshot({ path: resolve(output, 'result.png'), fullPage: true });
  await writeFile(resolve(output, 'receipt.json'), JSON.stringify({ scope: 'fixture-browser-ui', paid_model_calls: 0, exit_code: failed ? 1 : 0, checks, submissions, finishes, capability_reads: capabilityReads, held, page_errors: errors }, null, 2));
  console.log('Browser artifact: ' + output);
  await browser.close();
}
