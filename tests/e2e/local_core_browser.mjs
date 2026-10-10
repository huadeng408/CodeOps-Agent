import assert from 'node:assert/strict';
import { randomBytes, randomUUID, createHash } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, mkdtemp, readFile, writeFile, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { createServer } from 'node:net';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '../../frontend/node_modules/playwright/index.mjs';

if (process.env.CODE_AGENT_RUN_LOCAL_CORE_E2E !== '1') {
  console.log('SKIP: set CODE_AGENT_RUN_LOCAL_CORE_E2E=1');
  process.exit(0);
}

const root = fileURLToPath(new URL('../../', import.meta.url));
const profile = process.env.CODE_AGENT_E2E_PROFILE === 'full-stack' ? 'full-stack' : 'local-core';
const runId = `${profile}-${randomUUID()}`;
const artifacts = path.join(root, 'output', 'playwright', runId);
await mkdir(artifacts, { recursive: true });
const workspaceChecks = process.env.CODE_AGENT_E2E_WORKSPACE === '1';
const publicInput = process.env.CODE_AGENT_E2E_PUBLIC_INPUT === 'chi-v5.2.3';
assert.ok(!publicInput || workspaceChecks, 'public input requires workspace checks');
const publicInputRevision = publicInput ? '9b9fb55def404397748a9fc7e044efe9db1d618e' : undefined;
assert.ok(!workspaceChecks || profile === 'local-core', 'workspace runtime requires the approved local profile');
// Keep generated Go source outside the project's module discovery; retain it
// after the run, with its input/outcome hashes in the receipt.
const workspaceRoot = workspaceChecks ? await mkdtemp(path.join(tmpdir(), `codeops-${runId}-`)) : artifacts;
if (workspaceChecks) {
  const relative = path.relative(root, workspaceRoot);
  assert.ok(path.isAbsolute(relative) || relative === '..' || relative.startsWith(`..${path.sep}`), 'fixture storage must remain outside the project module');
}
const repository = path.join(workspaceRoot, 'repository');
const taskStorage = path.join(workspaceRoot, 'task-storage');
const repoGit = args => {
  const result = spawnSync('git', ['-c', 'core.hooksPath=', '-c', 'core.fsmonitor=false', '-C', repository, ...args], {
    env: { ...process.env, GIT_OPTIONAL_LOCKS: '0', GIT_CONFIG_GLOBAL: process.platform === 'win32' ? 'NUL' : '/dev/null', GIT_CONFIG_NOSYSTEM: '1' }, encoding: 'utf8', windowsHide: true,
  });
  assert.equal(result.status, 0, `controlled fixture Git ${args[0]}`);
  return result.stdout;
};
if (workspaceChecks) {
  await mkdir(repository); await mkdir(taskStorage);
  repoGit(['init']);
  if (publicInput) {
    repoGit(['remote', 'add', 'origin', 'https://github.com/go-chi/chi.git']);
    repoGit(['fetch', '--depth=1', 'origin', publicInputRevision]);
    repoGit(['checkout', '--detach', publicInputRevision]);
    assert.equal(repoGit(['rev-parse', 'HEAD']).trim(), publicInputRevision);
    assert.ok((await readFile(path.join(repository, 'LICENSE'), 'utf8')).includes('MIT License'));
    assert.ok((await readFile(path.join(repository, 'go.mod'), 'utf8')).includes('github.com/go-chi/chi/v5'));
  }
  repoGit(['config', 'user.name', 'Fixture']); repoGit(['config', 'user.email', 'fixture@example.test']);
  await writeFile(path.join(repository, 'main.go'), 'package fixture\n// committed\n');
  await writeFile(path.join(repository, 'gone.py'), '# remove this file\n');
  await writeFile(path.join(repository, '.env'), 'private fixture, never copied\n');
  repoGit(['add', '.']); repoGit(['commit', '-m', 'fixture']);
  await writeFile(path.join(repository, 'main.go'), 'package fixture\n// staged\n'); repoGit(['add', 'main.go']);
  await writeFile(path.join(repository, 'main.go'), 'package fixture\n// current dirty content\n');
  await writeFile(path.join(repository, 'new.py'), '# new source\n');
  await writeFile(path.join(repository, 'empty.py'), '');
  const { unlink } = await import('node:fs/promises'); await unlink(path.join(repository, 'gone.py'));
}
const binary = path.join(artifacts, process.platform === 'win32' ? 'server.exe' : 'server');
const hash = async name => createHash('sha256').update(await readFile(name)).digest('hex');
const captureRepository = async () => workspaceChecks ? {
  head: repoGit(['rev-parse', 'HEAD']), status: repoGit(['status', '--porcelain=v1', '-z']), index: await hash(path.join(repository, '.git', 'index')),
  files: Object.fromEntries(await Promise.all(['main.go', 'new.py', 'empty.py', '.env'].map(async name => [name, await hash(path.join(repository, name))]))),
} : null;
const repositoryBefore = await captureRepository();
let workspaceId, workspaceSessionId, workspaceBaseline;
const git = args => spawnSync('git', args, { cwd: root, encoding: 'utf8' }).stdout.trim();
const sourceNames = () => [...new Set([...git(['ls-files']).split('\n'), ...git(['ls-files', '--others', '--exclude-standard']).split('\n')])]
  .filter(name => /\.(go|proto)$/.test(name) || /^(tests\/e2e\/(local_core|full_stack)|frontend\/(src\/|package|tsconfig|vite.config)|configs\/local-core.yaml$|go\.(mod|sum)$)/.test(name)).sort();
const captureSources = async () => Object.fromEntries(await Promise.all(sourceNames().map(async name => [name, await hash(path.join(root, name))])));
const captureAssets = async () => {
  const files = ['index.html', ...(await readdir(path.join(root, 'frontend', 'dist', 'assets'))).sort().map(name => `assets/${name}`)];
  return Object.fromEntries(await Promise.all(files.map(async name => [name, await hash(path.join(root, 'frontend', 'dist', name))])));
};
const sourceHashes = await captureSources();
const assetHashes = await captureAssets();
const gitSha = git(['rev-parse', 'HEAD']);
const build = spawnSync('go', ['build', '-o', binary, './cmd/server'], { cwd: root, encoding: 'utf8' });
await writeFile(path.join(artifacts, 'build.log'), build.stdout + build.stderr);
assert.equal(build.status, 0, 'production Go server must compile');
const listener = createServer();
listener.listen(0, '127.0.0.1');
await once(listener, 'listening');
const port = listener.address().port;
await new Promise((resolve, reject) => listener.close(error => error ? reject(error) : resolve()));
const base = `http://127.0.0.1:${port}`;
const secret = randomBytes(32).toString('hex');
const password = randomBytes(16).toString('hex');
const username = 'browser-operator@example.test';
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) =>
  !/^(MYSQL_|REDIS_|MINIO_|JWT_SECRET$|CODEAGENT_|CODE_AGENT_|OPENAI_|ANTHROPIC_|LLM_|OTEL_)/i.test(key)));
if (workspaceChecks) {
  for (const key of Object.keys(env)) if (key.toUpperCase() === 'PATH') delete env[key];
  env.PATH = '';
}
const configPath = path.join(artifacts, 'local.yaml');
const ledgerPath = process.env.CODE_AGENT_E2E_LEDGER || path.join(artifacts, 'sessions.sqlite');
const yamlPath = value => JSON.stringify(value.replaceAll('\\', '/'));
await writeFile(configPath, `server:
  profile: ${profile}
  port: "${port}"
  mode: test
  allowed_origins: ${base}
  frontend_dir: ${yamlPath(path.join(root, 'frontend', 'dist'))}
harness:
  session_ledger_path: ${yamlPath(ledgerPath)}
  identity_path: ${yamlPath(path.join(artifacts, 'identity', 'identity.sqlite'))}
${workspaceChecks ? `  repository_root: ${yamlPath(repository)}\n  task_workspace_root: ${yamlPath(taskStorage)}\n` : ''}
jwt:
  secret: \${JWT_SECRET:}
  access_token_expire_hours: 1
  refresh_token_expire_days: 1
database:
  mysql:
    dsn: \${MYSQL_DSN:}
  redis:
    addr: \${REDIS_ADDR:}
minio:
  endpoint: \${MINIO_ENDPOINT:}
  access_key_id: \${MINIO_ACCESS_KEY:}
  secret_access_key: \${MINIO_SECRET_KEY:}
  bucket_name: codeops-compat-e2e
elasticsearch:
  addresses: ${base}
log:
  level: error
  format: json
`);
Object.assign(env, { CODEAGENT_CONFIG: configPath, JWT_SECRET: secret,
  CODE_AGENT_LOCAL_SETUP_USER: username, CODE_AGENT_LOCAL_SETUP_PASSWORD: password,
  CODE_AGENT_ORCHESTRATOR_ADDR: `127.0.0.1:${port}` });
if (profile === 'full-stack') {
  for (const key of ['MYSQL_DSN', 'REDIS_ADDR', 'MINIO_ENDPOINT', 'MINIO_ACCESS_KEY', 'MINIO_SECRET_KEY']) {
    assert.ok(process.env[`CODE_AGENT_E2E_${key}`], `isolated full-stack prerequisite ${key} is required`);
    env[key] = process.env[`CODE_AGENT_E2E_${key}`];
  }
}
let server, serverExit, browser;
const processIds = [];
const processOutcomes = [];
const checks = [];
let serverOutput = '';
const start = async () => {
  server = spawn(binary, [], { cwd: artifacts, env, windowsHide: true });
  processIds.push(server.pid);
  server.stdout.on('data', chunk => { serverOutput += chunk; });
  server.stderr.on('data', chunk => { serverOutput += chunk; });
  serverExit = once(server, 'exit');
  for (let attempt = 0; attempt < 300; attempt++) {
    assert.equal(server.exitCode, null, 'server must remain alive during readiness');
    const response = await fetch(`${base}/healthz`).catch(() => null);
    if (response?.ok) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  assert.fail('production server readiness timed out');
};
const stop = async () => {
  if (!server) return;
  server.kill();
  const [code, signal] = await serverExit;
  processOutcomes.push({ pid: server.pid, code, signal, stopRequested: true });
  server = undefined;
};
const check = async (name, run) => {
  const result = { name, status: 'failed' };
  checks.push(result);
  await run();
  result.status = 'passed';
};
let exitCode = 0;
try {
  await start();
  browser = await chromium.launch({ headless: process.env.CODE_AGENT_BROWSER_HEADLESS === '1' });
  const context = await browser.newContext({ viewport: { width: 1440, height: 960 } });
  const page = await context.newPage();
  await check('unauthenticated history refused', async () => {
    assert.equal((await context.request.get(`${base}/api/v1/sessions`)).status(), 401);
    if (workspaceChecks) assert.equal((await context.request.post(`${base}/api/v1/sessions/unknown/task-workspace/prepare`, { data: { expectedSeq: 0, requestId: 'unauthenticated' } })).status(), 401);
  });
  await check('actual browser initializes approved identity and logs in', async () => {
    await page.goto(base);
    await page.locator('.login-card').waitFor();
    await page.screenshot({ path: path.join(artifacts, '01-login.png') });
    await page.locator('.login-tabs').getByRole('button', { name: 'Register' }).click();
    await page.getByPlaceholder('Your name').fill('Browser operator');
    await page.locator('input[type=email]').fill(username);
    await page.locator('input[type=password]').fill(password);
    await page.locator('button[type=submit]').click();
    await page.getByRole('button', { name: '新建代码任务' }).waitFor();
  });
  await check('create and open durable Session through the browser', async () => {
    await page.getByRole('button', { name: '新建代码任务' }).click();
    await page.getByRole('textbox', { name: '项目', exact: true }).fill('browser-core');
    await page.getByRole('textbox', { name: '标题', exact: true }).fill('Persistent browser task');
    if (workspaceChecks) await page.getByRole('textbox', { name: '工作目录', exact: true }).fill(repository);
    await page.getByRole('button', { name: '创建会话', exact: true }).click();
    await page.locator('.title-button').filter({ hasText: 'Persistent browser task' }).waitFor();
    await page.screenshot({ path: path.join(artifacts, '02-history.png') });
  });
  if (workspaceChecks) {
    await check('browser prepares current dirty/new/empty/deleted baseline in a managed workspace', async () => {
      await page.getByRole('button', { name: '准备隔离工作区', exact: true }).waitFor();
      await page.screenshot({ path: path.join(artifacts, 'workspace-01-before.png') });
      await page.getByRole('button', { name: '准备隔离工作区', exact: true }).click();
      await page.locator('.task-workspace-panel').getByText('已准备', { exact: true }).waitFor();
      const sessions = (await (await context.request.get(`${base}/api/v1/sessions`)).json()).data;
      workspaceSessionId = sessions.find(item => item.title === 'Persistent browser task').id;
      assert.ok(sessions.find(item => item.id === workspaceSessionId).userId > 2 ** 51, 'real durable local owner is not fixture owner 1');
      const response = await context.request.get(`${base}/api/v1/sessions/${workspaceSessionId}/task-workspace`);
      const { data } = await response.json();
      assert.equal(data.state, 'prepared'); workspaceId = data.workspaceId; workspaceBaseline = data.baseline.checksum;
      assert.ok(data.baseline.files.some(file => file.path === 'gone.py' && !file.exists));
      assert.ok(data.baseline.files.some(file => file.path === 'empty.py' && file.exists && file.size === 0));
      for (const name of ['main.go', 'new.py', 'empty.py']) assert.equal(await hash(path.join(taskStorage, workspaceId, name)), repositoryBefore.files[name]);
      await assert.rejects(readFile(path.join(taskStorage, workspaceId, '.env')), { code: 'ENOENT' });
      assert.ok(repoGit(['worktree', 'list', '--porcelain']).includes(workspaceId));
      assert.deepEqual(await captureRepository(), repositoryBefore);
      await page.locator('.task-workspace-panel summary').click();
      await page.locator('.task-workspace-files').getByText('gone.py', { exact: true }).waitFor();
      await page.screenshot({ path: path.join(artifacts, 'workspace-02-expanded.png') });
    });
    await check('retained workspace refuses a second request and an unapproved repository', async () => {
      const duplicate = await context.request.post(`${base}/api/v1/sessions/${workspaceSessionId}/task-workspace/prepare`, { data: { expectedSeq: 3, requestId: 'replace-existing' } });
      assert.equal(duplicate.status(), 409);
      const created = await context.request.post(`${base}/api/v1/sessions`, { data: { projectName: 'denied', title: 'Unapproved root', workingDir: artifacts } });
      const id = (await created.json()).data.id;
      assert.equal((await context.request.post(`${base}/api/v1/sessions/${id}/task-workspace/prepare`, { data: { expectedSeq: 1, requestId: 'denied' } })).status(), 400);
      assert.deepEqual(await captureRepository(), repositoryBefore);
    });
  }
  await check('capabilities separate available history from blocked execution', async () => {
    await page.getByRole('heading', { name: '运行能力', exact: true }).waitFor();
    for (const state of ['READY', 'BLOCKED', 'DEGRADED', 'UNKNOWN']) {
      await page.locator('.capability-panel').getByText(state, { exact: true }).first().waitFor();
    }
    await page.getByRole('button', { name: '重新检查能力', exact: true }).click();
    const response = await context.request.get(`${base}/api/v1/capabilities`);
    assert.equal(response.status(), 200);
    const { data } = await response.json();
    assert.equal(data.history.state, 'ready');
    assert.equal(data.execution.state, 'blocked');
    assert.equal(data.rag.state, 'degraded');
    assert.equal(data.trace.state, 'unknown');
    assert.equal(data.provider.state, 'blocked');
    assert.equal(data.sandbox.state, 'blocked');
    assert.equal(data.budget.tokens.limit, 100_000_000);
    assert.equal(data.budget.tokens.cost_status, 'unknown');
    await page.locator('.composer-meta').getByText('前提未满足', { exact: false }).waitFor();
  });
  await check('browser shows actual persisted token amounts and unknown price', async () => {
    const response = await context.request.get(`${base}/api/v1/capabilities`);
    const { data } = await response.json();
    const row = page.locator('.capability-row').filter({ hasText: 'token 批次' });
    const summary = row.getByText(`已用 ${data.budget.tokens.used.toLocaleString()} / ${data.budget.tokens.limit.toLocaleString()} tokens`, { exact: false });
    await summary.waitFor();
    assert.ok((await summary.innerText()).includes('费用未知'));
    await page.screenshot({ path: path.join(artifacts, '02-token-budget.png') });
  });
  await check('browser refuses execution while prerequisites are absent', async () => {
    await page.locator('textarea.input-box').fill('Inspect this repository');
    const refused = page.waitForResponse(response => response.url().endsWith('/messages') && response.request().method() === 'POST');
    await page.getByRole('button', { name: '发送', exact: true }).click();
    assert.equal((await refused).status(), 503);
    await page.getByRole('alert').waitFor();
    assert.equal(await page.locator('textarea.input-box').inputValue(), 'Inspect this repository');
    await page.screenshot({ path: path.join(artifacts, '03-execution-blocked.png') });
  });
  await check('unreachable capability service shows unknown rather than cached readiness', async () => {
    await stop();
    const sockets = new Set();
    const stalled = createServer(socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
    stalled.listen(port, '127.0.0.1');
    await once(stalled, 'listening');
    try {
      await page.getByRole('button', { name: '重新检查能力', exact: true }).click();
      await page.getByText('无法确认能力状态，请检查服务连接后重试。', { exact: true }).waitFor();
      const states = await page.locator('.capability-panel .capability-state').allTextContents();
      assert.equal(states.length, 8);
      assert.ok(states.every(state => state === 'UNKNOWN'));
      await page.screenshot({ path: path.join(artifacts, '04-capability-unknown.png') });
    } finally {
      for (const socket of sockets) socket.destroy();
      await new Promise(resolve => stalled.close(resolve));
    }
  });
  await check('separate Go process retains authenticated browser history', async () => {
    await stop();
    await start();
    await page.reload();
    await page.locator('.session-list').getByText('Persistent browser task', { exact: true }).waitFor();
    await page.locator('.session-list').getByText('Persistent browser task', { exact: true }).click();
    await page.locator('.title-button').filter({ hasText: 'Persistent browser task' }).waitFor();
    await page.locator('.capability-panel').getByText('BLOCKED', { exact: true }).first().waitFor();
    await page.screenshot({ path: path.join(artifacts, '05-restarted-history.png') });
  });
  if (workspaceChecks) {
    await check('new Go process restores the same lease and browser baseline', async () => {
      await page.locator('.task-workspace-panel').getByText('已准备', { exact: true }).waitFor();
      const { data } = await (await context.request.get(`${base}/api/v1/sessions/${workspaceSessionId}/task-workspace`)).json();
      assert.equal(data.workspaceId, workspaceId); assert.equal(data.baseline.checksum, workspaceBaseline); assert.equal(data.eventCount, 3);
      assert.deepEqual(await captureRepository(), repositoryBefore);
      await page.screenshot({ path: path.join(artifacts, 'workspace-03-restarted.png') });
    });
    await check('changed isolated content is blocked and retained without touching the source', async () => {
      await writeFile(path.join(taskStorage, workspaceId, 'main.go'), 'package fixture\n// deliberate fixture mutation\n');
      await page.getByRole('button', { name: '重新检查工作区', exact: true }).click();
      await page.locator('.task-workspace-panel').getByText('需处理', { exact: true }).waitFor();
      assert.deepEqual(await captureRepository(), repositoryBefore);
      assert.equal(await readFile(path.join(taskStorage, workspaceId, 'main.go'), 'utf8'), 'package fixture\n// deliberate fixture mutation\n');
      await page.screenshot({ path: path.join(artifacts, 'workspace-04-retained.png') });
    });
  }
  await check('browser logout removes access to history', async () => {
    await page.getByRole('button', { name: '退出登录', exact: true }).click();
    await page.locator('.login-card').waitFor();
    assert.equal((await context.request.get(`${base}/api/v1/sessions`)).status(), 401);
  });
} catch (error) {
  exitCode = 1;
  console.error(error.name + ': ' + String(error.message).replaceAll(password, '[REDACTED]').replaceAll(secret, '[REDACTED]'));
} finally {
  await browser?.close();
  await stop();
  for (const value of [secret, password, env.MYSQL_DSN, env.MINIO_ACCESS_KEY, env.MINIO_SECRET_KEY]) {
    if (value) serverOutput = serverOutput.replaceAll(value, '[REDACTED]');
  }
  await writeFile(path.join(artifacts, 'server.log'), serverOutput);
  const sourceChangedWhileRunning = JSON.stringify(sourceHashes) !== JSON.stringify(await captureSources());
  const assetsChangedWhileRunning = JSON.stringify(assetHashes) !== JSON.stringify(await captureAssets());
  if (sourceChangedWhileRunning || assetsChangedWhileRunning) exitCode = 1;
  const artifactsHashes = Object.fromEntries(await Promise.all((await readdir(artifacts)).filter(name => /\.(png|log)$/.test(name)).map(async name => [name, await hash(path.join(artifacts, name))])));
  const denominator = workspaceChecks ? 13 : 9;
  const receipt = { runId, scope: `production ${profile} browser authentication/history, no model execution`,
    gitSha, sourceHashes, sourceChangedWhileRunning, assetHashes, assetsChangedWhileRunning, binarySha256: await hash(binary), buildExitCode: build.status, processIds, processOutcomes,
    command: profile === 'full-stack' ? 'CODE_AGENT_RUN_LOCAL_CORE_E2E=1 pwsh -File tests/e2e/full_stack_startup.ps1' : `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 ${workspaceChecks ? 'CODE_AGENT_E2E_WORKSPACE=1 ' : ''}${publicInput ? 'CODE_AGENT_E2E_PUBLIC_INPUT=chi-v5.2.3 ' : ''}${process.env.CODE_AGENT_BROWSER_HEADLESS === '1' ? 'CODE_AGENT_BROWSER_HEADLESS=1 ' : ''}node tests/e2e/local_core_browser.mjs`, exitCode,
    denominator, checks, notRun: denominator - checks.length, artifactsHashes, traceBackend: 'unknown', modelCalls: 0,
    workspaceChecks, nativeGitAvailableToServer: workspaceChecks ? false : undefined, workspaceRoot, repositoryBefore, repositoryAfter: await captureRepository(), workspaceId, workspaceBaseline,
    publicInputRevision, publicInputRepository: publicInput ? 'https://github.com/go-chi/chi' : undefined,
    workspaceInput: workspaceChecks ? (publicInput ? 'pinned public checkout with controlled dirty/new/empty/deleted inputs; no code task executed' : 'controlled fixture repository; not a coding task') : undefined };
  await writeFile(path.join(artifacts, 'receipt.json'), JSON.stringify(receipt, null, 2) + '\n');
  console.log(JSON.stringify({ runId, passed: checks.filter(result => result.status === 'passed').length, denominator, exitCode }));
}
process.exitCode = exitCode;
