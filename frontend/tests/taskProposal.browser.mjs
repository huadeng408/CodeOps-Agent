// Public React/Chromium seam; HTTP and WebSocket responses are external fixtures.
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { chromium } from 'playwright';

const output = resolve('../output/playwright/task-proposal-' + Date.now());
const recovery = process.argv.includes('--reconcile');
const partial = process.argv.includes('--partial') || recovery;
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const session = {
  id: 'proposal-fixture', userId: 7, projectName: 'CodeOps-Agent', title: 'Proposal fixture',
  goal: 'Review isolated changes', workingDir: '.', status: 'done', eventCount: 17,
  createdAt: '2026-10-13T00:00:00Z', updatedAt: '2026-10-13T00:00:00Z',
};
const proposal = {
  id: 'proposal-1', sessionId: session.id, workspaceId: 'workspace-1', baselineChecksum: 'baseline-1',
  sourceEventChecksum: 'event-17', finalStateSHA256: 'a'.repeat(64), state: 'ready',
  files: [{ path: 'hello.go', beforeExists: true, afterExists: true, beforeSha256: 'before-1', afterSha256: 'after-1', beforeMode: 420, afterMode: 420,
    diff: '--- a/hello.go\n+++ b/hello.go\n@@ -1 +1 @@\n-return "old"\n+return "fixed"\n' }],
  test: { state: 'passed', command: ['go', 'test', './...', '-count=1'], inputSHA256: 'a'.repeat(64),
    archiveSHA256: 'b'.repeat(64), modeTransform: 'readonly-v1', runtimeId: 'runtime-1', exitCode: 0,
    output: 'ok fixture/module 0.01s', truncated: false, stopped: true },
};
const workspace = { available: false, state: 'prepared', repository: 'fixture/module', workspaceId: 'workspace-1',
  eventCount: 17, testAvailable: false, testReason: 'baseline already retained', proposalAvailable: true };
let retained = partial ? { ...proposal, files: [
  { ...proposal.files[0], operation: 'edit' },
  { ...proposal.files[0], path: 'empty.txt', operation: 'delete', afterExists: false, afterMode: 0, diff: '' },
  { ...proposal.files[0], path: 'new.txt', operation: 'create', beforeExists: false, beforeMode: 0, diff: '' },
  { ...proposal.files[0], path: 'old.txt', destination: 'moved.txt', operation: 'rename', diff: '' },
] } : undefined;
let application = partial ? { requestId: 'application-1', proposalId: proposal.id, decision: 'approve', state: 'partial', reason: 'item_intent_not_committed',
  items: retained.files.map((file, index) => ({ path: file.path, destination: file.destination, operation: file.operation, state: index === 0 ? 'applied' : 'not_started' })) } : undefined;
const attempts = [], errors = [];
const checks = partial ? { partial_status: false, complete_item_denominator: false, no_blind_retry: false, refresh_retains_partial: false, no_page_errors: false }
  : { generation: false, review_diff_and_binding: false, refresh_retains_result: false, regeneration_after_stale: false, no_apply: false, no_page_errors: false };
if (recovery) {checks.explicit_readonly_reconciliation = false; checks.retained_identity = false;}
let failed = true;
page.on('pageerror', error => errors.push(error.message));
try {
  await page.addInitScript(() => localStorage.setItem('task-proposal-request:proposal-fixture', 'retained-proposal-request'));
  await page.route('**/healthz', route => route.fulfill({ json: { continuation: { attached: true, consecutive_failures: 0 } } }));
  await page.routeWebSocket(/\/api\/v1\/sessions\/.+\/ws\?/, () => {});
  await page.route('**/api/v1/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname;
    let data = [];
    if (path.endsWith('/users/me')) data = { id: 7, username: 'fixture' };
    else if (path === '/api/v1/sessions') data = [session];
    else if (path === '/api/v1/sessions/' + session.id) data = session;
    else if (path.endsWith('/ws-ticket')) data = { ticket: 'fixture' };
    else if (path.endsWith('/task-workspace/proposal/reconcile')) {
      attempts.push({ method: request.method(), path, body: request.postDataJSON() });
      application = { ...application, reconciled: true, observedChecksum: 'c'.repeat(64) };
      data = { ...workspace, available: true, state: 'blocked', proposal: retained, application, eventCount: 18 };
    }
    else if (path.endsWith('/task-workspace/proposal')) {
      attempts.push({ method: request.method(), path, body: request.postDataJSON() });
      retained = { ...proposal, id: attempts.length === 1 ? 'proposal-1' : 'proposal-2' };
      data = { ...workspace, proposalAvailable: false, eventCount: 18, proposal: retained };
    } else if (path.endsWith('/task-workspace')) data = { ...workspace, available: recovery, state: partial ? 'blocked' : workspace.state,
      proposalAvailable: !retained || retained.state === 'stale' && retained.test.stopped, proposal: retained, application };
    await route.fulfill({ json: { data } });
  });
  await page.goto(process.argv[2] || 'http://127.0.0.1:3001/');
  await page.locator('textarea.input-box').waitFor();
  await page.getByRole('region', { name: '隔离工作区', exact: true }).getByText('fixture/module', { exact: true }).waitFor();
  if (partial) {
    const card = page.getByRole('region', { name: '代码修改提案', exact: true });
    await card.getByText('部分应用，需对账', { exact: true }).waitFor();
    assert.equal(await card.getByText('已应用，原 index 保留', { exact: true }).count(), 0);
    checks.partial_status = true;
    const items = card.getByRole('list', { name: '逐项应用结果' });
    assert.equal(await items.getByRole('listitem').count(), 4);
    assert.equal(await items.getByText('已完成', { exact: true }).count(), 1);
    assert.equal(await items.getByText('未执行', { exact: true }).count(), 3);
    checks.complete_item_denominator = true;
    assert.equal(await card.getByRole('button', { name: /确认应用|拒绝此提案/ }).count(), 0);
    checks.no_blind_retry = true;
    await page.getByRole('button', { name: '重新检查工作区', exact: true }).click();
    await card.getByText('部分应用，需对账', { exact: true }).waitFor();
    assert.equal(attempts.length, 0);
    checks.refresh_retains_partial = true;
    if (recovery) {
      await card.getByRole('button', { name: '核验应用结果', exact: true }).click();
      await card.getByText(/已核验原副本，未继续执行旧应用/).waitFor();
      assert.equal(attempts.length, 1);
      assert.equal(attempts[0].body.applicationRequestId, 'application-1');
      assert.equal(attempts[0].body.expectedSeq, 17);
      assert.deepEqual(Object.keys(attempts[0].body).sort(), ['applicationRequestId', 'expectedSeq', 'requestId']);
      checks.explicit_readonly_reconciliation = true;
      assert.ok(attempts[0].body.requestId);
      await page.getByRole('button', { name: '重新检查工作区', exact: true }).click();
      await card.getByText('部分应用，需对账', { exact: true }).waitFor();
      assert.equal(attempts.length, 1);
      checks.retained_identity = true;
    }
  } else {
  const generate = page.getByRole('button', { name: '生成并测试提案', exact: true });
  assert.equal(await generate.count(), 1, 'completed task exposes no proposal generation control');
  await generate.click();
  const card = page.getByRole('region', { name: '代码修改提案', exact: true });
  await card.getByText('通过', { exact: true }).waitFor();
  assert.deepEqual(attempts, [{ method: 'POST', path: '/api/v1/sessions/proposal-fixture/task-workspace/proposal',
    body: { expectedSeq: 17, requestId: 'retained-proposal-request' } }]);
  checks.generation = true;
  await card.getByText('hello.go', { exact: true }).click();
  assert.equal(await card.getByLabel('hello.go 修改 diff').textContent(), proposal.files[0].diff);
  await card.getByText('go test ./... -count=1', { exact: true }).waitFor();
  await card.getByText(/退出码：0/).waitFor();
  await card.getByText('测试输入：' + proposal.test.inputSHA256, { exact: true }).waitFor();
  await card.getByText('归档 SHA-256：' + proposal.test.archiveSHA256, { exact: true }).waitFor();
  await card.getByText(/readonly-v1/).waitFor();
  checks.review_diff_and_binding = true;
  await page.getByRole('button', { name: '重新检查工作区', exact: true }).click();
  await card.getByText('通过', { exact: true }).waitFor();
  assert.equal(attempts.length, 1);
  checks.refresh_retains_result = true;
  assert.equal(await card.getByRole('button', { name: /应用/ }).count(), 0);
  checks.no_apply = true;
  retained = { ...retained, state: 'stale' };
  await page.getByRole('button', { name: '重新检查工作区', exact: true }).click();
  const regenerate = page.getByRole('button', { name: '重新生成并测试提案', exact: true });
  await regenerate.waitFor({ state: 'visible', timeout: 3000 });
  assert.equal(await regenerate.count(), 1, 'a confirmed stopped stale proposal cannot be regenerated');
  await regenerate.click();
  await card.getByText('proposal-2', { exact: true }).waitFor();
  assert.equal(attempts.length, 2);
  assert.notEqual(attempts[1].body.requestId, attempts[0].body.requestId, 'new frozen inputs reused the completed proposal request');
  checks.regeneration_after_stale = true;
  }
  assert.deepEqual(errors, []);
  checks.no_page_errors = true;
  await page.screenshot({ path: resolve(output, 'review.png'), fullPage: true });
  failed = false;
} catch (error) {
  await page.screenshot({ path: resolve(output, 'failed.png'), fullPage: true });
  throw error;
} finally {
  await writeFile(resolve(output, 'receipt.json'), JSON.stringify({ scope: 'fixture-browser-ui', partial_application: partial, provider_backed: false,
    paid_model_calls: 0, exit_code: failed ? 1 : 0, checks, attempts, page_errors: errors }, null, 2));
  console.log('Browser artifact: ' + output);
  await browser.close();
}
