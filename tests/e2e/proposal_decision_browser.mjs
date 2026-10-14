import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { chromium } from '../../frontend/node_modules/playwright/index.mjs';

const { CODE_AGENT_DECISION_URL: base, CODE_AGENT_DECISION_PASSWORD: password,
  CODE_AGENT_DECISION_SESSION: sid, CODE_AGENT_DECISION_PROPOSAL: proposalId,
  CODE_AGENT_DECISION_ACTION: action, CODE_AGENT_DECISION_ARTIFACT: artifact } = process.env;
const lifecycle = action === 'lifecycle', decision = lifecycle ? 'approve' : action;
assert.ok(base && password && sid && proposalId && artifact);
assert.ok(['approve', 'reject'].includes(decision));
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 960 } });
const page = await context.newPage();
const errors = [], checks = {};
page.on('pageerror', error => errors.push(error.message));
let payload, failed = true;
try {
  const login = await context.request.post(base + '/api/v1/users/login', { data: { email: 'operator@example.test', password } });
  assert.equal(login.status(), 200);
  await page.goto(base);
  await page.getByRole('region', { name: '代码修改提案', exact: true }).getByText(proposalId, { exact: true }).waitFor({ timeout: 20000 });
  const card = page.getByRole('region', { name: '代码修改提案', exact: true });
  await card.getByText('calc.go', { exact: true }).click();
  assert.ok((await card.getByLabel('calc.go 修改 diff').textContent()).includes('return left + right'));
  checks.review = true;
  if (lifecycle) {
    for (const label of ['新增', '删除', '重命名']) assert.equal(await card.locator('summary').filter({ hasText: ' · ' + label }).count(), 1);
    await card.getByText('nested/new.txt', { exact: true }).click();
    await card.getByText('old.txt', { exact: true }).click();
    assert.ok((await card.getByLabel('old.txt 修改 diff').textContent()).includes('b/moved.txt'));
    checks.lifecycle_preview = true;
  }
  const approveButton = lifecycle ? '确认应用这 4 项修改' : '确认应用此文件';
  const [response] = await Promise.all([
    page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/task-workspace/proposal/decision')),
    card.getByRole('button', { name: decision === 'approve' ? approveButton : '拒绝此提案', exact: true }).click(),
  ]);
  assert.equal(response.status(), 200);
  payload = response.request().postDataJSON();
  assert.equal(payload.proposalId, proposalId);
  assert.equal(payload.decision, decision);
  const state = decision === 'approve' ? 'applied' : 'rejected';
  const application = (await response.json()).data.application;
  assert.equal(application.state, state);
  if (lifecycle) {
    assert.equal(application.items.length, 4);
    assert.ok(application.items.every(item => item.state === 'applied'));
    await card.getByRole('list', { name: '逐项应用结果' }).getByText('已完成', { exact: true }).first().waitFor();
    assert.equal(await card.getByRole('list', { name: '逐项应用结果' }).getByRole('listitem').count(), 4);
    checks.item_outcomes = true;
  }
  await card.getByText(decision === 'approve' ? '已应用，原 index 保留' : '已拒绝，隔离结果保留', { exact: true }).waitFor();
  checks.decision = true;
  const replay = await context.request.post(base + `/api/v1/sessions/${sid}/task-workspace/proposal/decision`, { data: { ...payload, expectedSeq: 0 } });
  assert.equal(replay.status(), 200);
  assert.equal((await replay.json()).data.application.state, state);
  checks.replay = true;
  await page.getByRole('button', { name: '重新检查工作区', exact: true }).click();
  await card.getByText(decision === 'approve' ? '已应用，原 index 保留' : '已拒绝，隔离结果保留', { exact: true }).waitFor();
  assert.equal(await card.getByRole('button', { name: approveButton, exact: true }).count(), 0);
  assert.equal(await card.getByRole('button', { name: '拒绝此提案', exact: true }).count(), 0);
  checks.refresh_no_second_decision = true;
  assert.deepEqual(errors, []);
  checks.no_page_errors = true;
  await page.screenshot({ path: artifact + '.png', fullPage: true });
  failed = false;
} finally {
  await writeFile(artifact, JSON.stringify({ scope: 'production-go-http-browser-decision', external_model_and_orchestrator_fixture: true,
    paid_model_calls: 0, decision, lifecycle, session_id: sid, proposal_id: proposalId, exit_code: failed ? 1 : 0, checks, page_errors: errors }, null, 2), { flag: 'wx' });
  await browser.close();
}
