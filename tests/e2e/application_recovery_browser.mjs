import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { chromium } from '../../frontend/node_modules/playwright/index.mjs';

const { CODE_AGENT_DECISION_URL: base, CODE_AGENT_DECISION_PASSWORD: password,
  CODE_AGENT_DECISION_SESSION: sid, CODE_AGENT_DECISION_PROPOSAL: proposalId,
  CODE_AGENT_RECOVERY_VIEW: input, CODE_AGENT_DECISION_ARTIFACT: artifact,
  CODE_AGENT_RECOVERY_APPROVE: approve, CODE_AGENT_RECOVERY_CONFLICT: conflict } = process.env;
assert.ok(base && password && sid && proposalId && input && artifact);
const { view } = JSON.parse(await readFile(input, 'utf8'));
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 960 } });
const page = await context.newPage();
const errors = [], checks = {};
page.on('pageerror', error => errors.push(error.message));
let failed = true;
const reconciliationAttempts = [], approvalAttempts = [];
try {
  const login = await context.request.post(base + '/api/v1/users/login', { data: { email: 'operator@example.test', password } });
  assert.equal(login.status(), 200);
  await page.goto(base);
  const card = page.getByRole('region', { name: '代码修改提案', exact: true });
  await card.getByText(proposalId, { exact: true }).waitFor({ timeout: 20000 });
  assert.equal(await card.locator('details').count(), view.proposal.files.length);
  checks.retained_proposal = true;
  if (conflict === '1') {
    const [response] = await Promise.all([
      page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/proposal/decision')),
      card.getByRole('button', { name: '确认应用这 4 项修改', exact: true }).click(),
    ]);
    assert.equal(response.status(), 409);
    await page.getByText('原基线、授权或会话已变化，请重新检查；不会覆盖现有内容。', { exact: true }).waitFor();
    await page.getByRole('button', { name: '重新检查工作区', exact: true }).click();
    await card.getByText(proposalId, { exact: true }).waitFor();
    checks.concurrent_conflict_retained = true;
  } else if (approve === '1') {
    for (let attempt = 0; attempt < 3; attempt++) {
      const [response] = await Promise.all([
        page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/proposal/decision')),
        card.getByRole('button', { name: `确认应用这 ${view.proposal.files.length} 项修改`, exact: true }).click(),
      ]);
      approvalAttempts.push({ payload: response.request().postDataJSON(), status: response.status() });
      assert.equal(approvalAttempts.at(-1).payload.requestId, approvalAttempts[0].payload.requestId);
      if (response.status() === 200) {
        assert.equal(response.request().postDataJSON().decision, 'approve');
        assert.equal((await response.json()).data.application.state, 'applied');
        break;
      }
      assert.equal(response.status(), 409);
      assert.ok(attempt < 2, 'approval cursor did not settle');
      await page.getByRole('button', { name: '重新检查工作区', exact: true }).click();
      await card.getByText(proposalId, { exact: true }).waitFor();
    }
    checks.new_explicit_approval = true;
  } else if (view.application) {
    const stateLabels = { applied: '已应用，原 index 保留', partial: '部分应用，需对账', conflict: '应用冲突，现有内容保留', unknown: '应用结果待核验，请先对账' };
    await card.getByText(stateLabels[view.application.state], { exact: true }).waitFor();
    checks.parent_state = true;
    const items = card.getByRole('list', { name: '逐项应用结果' });
    assert.equal(await items.getByRole('listitem').count(), 4);
    const labels = { applied: '已完成', not_started: '未执行', not_applied: '当前未应用', unknown: '待核验', pending: '待核验', conflict: '冲突' };
    for (let index = 0; index < 4; index++) assert.ok((await items.getByRole('listitem').nth(index).textContent()).includes(labels[view.application.items[index].state]));
    checks.item_states = true;
    if (['partial', 'conflict', 'unknown'].includes(view.application.state)) {
      for (let attempt = 0; attempt < 3; attempt++) {
        const [response] = await Promise.all([
          page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/proposal/reconcile')),
          card.getByRole('button', { name: '核验应用结果', exact: true }).click(),
        ]);
        const payload = response.request().postDataJSON();
        reconciliationAttempts.push({ payload, status: response.status() });
        assert.deepEqual(Object.keys(payload).sort(), ['applicationRequestId', 'expectedSeq', 'requestId']);
        assert.equal(payload.applicationRequestId, view.application.requestId);
        assert.equal(payload.requestId, reconciliationAttempts[0].payload.requestId);
        if (response.status() === 200) {
          const result = (await response.json()).data.application;
          assert.equal(result.reconciled, true);
          assert.equal(result.state, view.application.state);
          break;
        }
        assert.equal(response.status(), 409);
        assert.ok(attempt < 2, 'reconciliation cursor did not settle');
        await page.getByRole('button', { name: '重新检查工作区', exact: true }).click();
        await card.getByText(proposalId, { exact: true }).waitFor();
      }
      checks.explicit_read_only_reconciliation = true;
      await card.getByText(/在同一仓库新建会话/).waitFor();
      checks.new_baseline_guidance = true;
    }
    assert.equal(await card.getByRole('button', { name: /确认应用/ }).count(), 0);
    checks.no_old_approval = true;
  }
  await page.getByRole('button', { name: '重新检查工作区', exact: true }).click();
  await card.getByText(proposalId, { exact: true }).waitFor();
  checks.refresh_retains_proposal = true;
  assert.deepEqual(errors, []);
  checks.no_page_errors = true;
  await page.screenshot({ path: artifact + '.png', fullPage: true });
  failed = false;
} finally {
  await writeFile(artifact, JSON.stringify({ scope: 'production-go-http-browser-recovery', paid_model_calls: 0,
    external_model_and_orchestrator_fixture: true, session_id: sid, proposal_id: proposalId,
    exit_code: failed ? 1 : 0, checks, reconciliation_attempts: reconciliationAttempts, approval_attempts: approvalAttempts, page_errors: errors }, null, 2), { flag: 'wx' });
  await browser.close();
}
