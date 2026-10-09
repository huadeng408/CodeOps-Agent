import assert from 'node:assert/strict';
import { randomBytes, randomUUID, createHash } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, readFile, writeFile, readdir } from 'node:fs/promises';
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
const binary = path.join(artifacts, process.platform === 'win32' ? 'server.exe' : 'server');
const hash = async name => createHash('sha256').update(await readFile(name)).digest('hex');
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
  !/^(MYSQL_|REDIS_|MINIO_|JWT_SECRET$|CODEAGENT_|CODE_AGENT_|OPENAI_|ANTHROPIC_|LLM_|OTEL_)/.test(key)));
const configPath = path.join(artifacts, 'local.yaml');
const yamlPath = value => JSON.stringify(value.replaceAll('\\', '/'));
await writeFile(configPath, `server:
  profile: ${profile}
  port: "${port}"
  mode: test
  allowed_origins: ${base}
  frontend_dir: ${yamlPath(path.join(root, 'frontend', 'dist'))}
harness:
  session_ledger_path: ${yamlPath(path.join(artifacts, 'sessions.sqlite'))}
  identity_path: ${yamlPath(path.join(artifacts, 'identity', 'identity.sqlite'))}
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
    await page.getByRole('button', { name: '创建会话', exact: true }).click();
    await page.locator('.title-button').filter({ hasText: 'Persistent browser task' }).waitFor();
    await page.screenshot({ path: path.join(artifacts, '02-history.png') });
  });
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
    await page.locator('.composer-meta').getByText('前提未满足', { exact: false }).waitFor();
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
      const states = await page.locator('.capability-state').allTextContents();
      assert.equal(states.length, 5);
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
    await page.locator('.capability-panel').getByText('BLOCKED', { exact: true }).waitFor();
    await page.screenshot({ path: path.join(artifacts, '05-restarted-history.png') });
  });
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
  const denominator = 8;
  const receipt = { runId, scope: `production ${profile} browser authentication/history, no model execution`,
    gitSha, sourceHashes, sourceChangedWhileRunning, assetHashes, assetsChangedWhileRunning, binarySha256: await hash(binary), buildExitCode: build.status, processIds, processOutcomes,
    command: profile === 'full-stack' ? 'CODE_AGENT_RUN_LOCAL_CORE_E2E=1 pwsh -File tests/e2e/full_stack_startup.ps1' : 'CODE_AGENT_RUN_LOCAL_CORE_E2E=1 node tests/e2e/local_core_browser.mjs', exitCode,
    denominator, checks, notRun: denominator - checks.length, artifactsHashes, traceBackend: 'unknown', modelCalls: 0 };
  await writeFile(path.join(artifacts, 'receipt.json'), JSON.stringify(receipt, null, 2) + '\n');
  console.log(JSON.stringify({ runId, passed: checks.filter(result => result.status === 'passed').length, denominator, exitCode }));
}
process.exitCode = exitCode;
