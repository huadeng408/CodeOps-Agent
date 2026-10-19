// Public browser UI regression; HTTP/WebSocket are external fixtures.
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { chromium } from 'playwright';

const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const output = resolve('../.scratch/model-task-finish/browser-' + Date.now());
await mkdir(output, { recursive: true });
const session = {
  id: 'finish-fixture', userId: 7, title: 'Finish task fixture', projectName: 'CodeOps-Agent',
  goal: 'Finish without resetting budget', workingDir: '.', status: 'done', eventCount: 1,
  createdAt: '2026-10-12T00:00:00Z', updatedAt: '2026-10-12T00:00:00Z',
  run: { sessionId: 'finish-fixture', runId: 'fixture-run', requestId: 'fixture-request', status: 'completed', attempt: 1, checkpointHash: '' },
};
const other = { ...session, id: 'other-fixture', title: 'Other task fixture', run: undefined };
let held = true, blocked = false, exhausted = false, refuse = true;
let delayRefusal, releaseRefusal;
const attempts = [], errors = [];
const checks = { owned_control: false, refusal_retained: false, active_call_disabled: false, refusal_session_bound: false, exhausted_finish_enabled: false, finished_preserves_budget: false, no_page_errors: false };
let failed = true;
page.on('pageerror', error => errors.push(error.message));
try {
  await page.route('**/healthz', route => route.fulfill({ json: { continuation: { attached: true, consecutive_failures: 0 } } }));
  await page.routeWebSocket(/\/api\/v1\/sessions\/.+\/ws\?/, () => {});
  await page.route('**/api/v1/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname;
    let data = [];
    if (path.endsWith('/users/me')) data = { id: 7, username: 'fixture' };
    else if (path === '/api/v1/sessions') data = [session, other];
    else if (path === '/api/v1/sessions/' + session.id) data = session;
    else if (path === '/api/v1/sessions/' + other.id) data = other;
    else if (path.endsWith('/ws-ticket')) data = { ticket: 'fixture' };
    else if (path.endsWith('/task-workspace')) data = { available: false, state: 'unprepared', reason: 'fixture execution unavailable' };
    else if (path.endsWith('/capabilities')) data = {
      budget: { state: blocked || exhausted ? 'blocked' : 'ready', reason: blocked ? 'a model attempt is still active; wait or recover its execution' : exhausted ? 'token batch is exhausted' : 'provider enforces budget; local usage is audited',
        tokens: { batch_id: 'original-fixture-batch', task_id: held ? session.id : undefined, limit: 100_000_000, used: exhausted ? 100_000_000 : 11, reserved: blocked ? 103 : 0, cost_status: 'unknown' } },
      execution: { state: 'blocked', reason: 'model credentials are missing' },
    };
    else if (path.endsWith('/task/finish')) {
      attempts.push({ method: request.method(), path });
      if (delayRefusal) await delayRefusal;
      if (refuse) return route.fulfill({ status: 409, json: { message: 'session state conflict' } });
      held = false;
      data = { sessionId: session.id };
    }
    await route.fulfill({ json: { data } });
  });
  await page.goto(process.argv[2] || 'http://127.0.0.1:3000/');
  await page.locator('textarea.input-box').waitFor();
  await page.getByText(/已确认 11 tokens，批准上限 100,000,000/).waitFor();
  const finish = page.getByRole('button', { name: '结束代码任务', exact: true });
  assert.equal(await finish.count(), 1, 'owned task has no completion control');
  checks.owned_control = true;
  await finish.click();
  await page.getByText('任务仍在运行或工具尚未确认停止；等待完成或对账后再结束。', { exact: true }).waitFor();
  assert.equal(held, true);
  assert.equal(attempts.length, 1);
  checks.refusal_retained = true;
  blocked = true;
  await page.getByRole('button', { name: '重新检查能力', exact: true }).click();
  await page.getByText(/原预留估计 103/).waitFor();
  assert.equal(await finish.isDisabled(), true);
  assert.equal(attempts.length, 1);
  checks.active_call_disabled = true;
  blocked = false;
  await page.getByRole('button', { name: '重新检查能力', exact: true }).click();
  await page.getByText(/原预留估计 0/).waitFor();
  delayRefusal = new Promise(resolve => { releaseRefusal = resolve; });
  await finish.click();
  await page.getByRole('button', { name: /Other task fixture/ }).click();
  const refusedResponse = page.waitForResponse(response => response.url().endsWith('/task/finish') && response.status() === 409);
  releaseRefusal();
  await refusedResponse;
  assert.equal(await page.getByText('任务仍在运行或工具尚未确认停止；等待完成或对账后再结束。', { exact: true }).count(), 0);
  assert.equal(await finish.count(), 0);
  assert.equal(held, true);
  checks.refusal_session_bound = true;
  delayRefusal = undefined;
  await page.getByRole('button', { name: /Finish task fixture/ }).click();
  exhausted = true; refuse = false;
  await page.getByRole('button', { name: '重新检查能力', exact: true }).click();
  await page.getByText(/已确认 100,000,000 tokens，批准上限 100,000,000/).waitFor();
  assert.equal(await finish.isEnabled(), true);
  checks.exhausted_finish_enabled = true;
  await finish.click();
  await page.getByText('代码任务已结束。历史和用量记录保留；后续调用仍受服务商预算上限约束。', { exact: true }).waitFor();
  assert.equal(await finish.count(), 0);
  assert.equal(attempts.length, 3);
  assert.ok(attempts.every(x => x.method === 'POST' && x.path === '/api/v1/sessions/finish-fixture/task/finish'));
  await page.getByText(/已确认 100,000,000 tokens，批准上限 100,000,000/).waitFor();
  checks.finished_preserves_budget = true;
  assert.deepEqual(errors, []);
  checks.no_page_errors = true;
  await page.screenshot({ path: resolve(output, 'finished.png'), fullPage: true });
  failed = false;
} catch (error) {
  await page.screenshot({ path: resolve(output, 'failed.png'), fullPage: true });
  throw error;
} finally {
  await writeFile(resolve(output, 'receipt.json'), JSON.stringify({ scope: 'fixture-browser-ui', provider_backed: false, paid_model_calls: 0, exit_code: failed ? 1 : 0, checks, attempts, page_errors: errors }, null, 2));
  console.log('Browser artifact: ' + output);
  await browser.close();
}
