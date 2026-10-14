import { useCallback, useEffect, useRef, useState } from 'react';
import { api, ApiError } from './api';
import { createContinuationRequestId } from './continuationRequest';
import type { TaskWorkspace } from './types';

const labels = { unprepared: '待准备', prepared: '已准备', blocked: '需处理', unknown: '待核验' };
const reasons: Record<string, string> = {
  'task repository and storage are not approved': '请在本机配置中批准仓库和隔离存储目录。',
  'repository is not approved for task preparation': '当前会话的仓库未获批准，请核对仓库选择和本机配置。',
  'repository approval changed; retained workspace requires reconciliation': '仓库授权已变化，隔离结果仍保留，请先核对配置。',
  'preparation outcome is unknown; retained lease requires reconciliation': '准备曾中断，结果未知；副本和记录保留，需要核对。',
  'preparation failed; retained lease requires reconciliation': '准备失败，副本和记录保留，需要核对。',
  'preparation lease expired; retained workspace requires reconciliation': '准备租约已过期，副本保留，需要重新核验。',
  'task copy or Git registration changed; retained workspace requires reconciliation': '副本或 Git 登记发生变化，先核对结果再继续。',
  'local task runtime is not attached': '请先在本机配置中启用已批准的 OpenSandbox 测试环境。',
  'approved OpenSandbox prerequisites are unavailable': 'OpenSandbox 前提未通过核验，请检查本机服务、镜像和授权配置。',
  'test outcome is unknown; reconcile the retained operation': '测试结果未知，执行标识和记录已保留；请先核验，避免重复运行。',
  'task admission is unavailable; reconcile the retained operation': '任务槽暂不可用，请先核对已有任务；本次请求已保留。',
  'repository or task baseline changed; reconcile the retained result': '仓库或隔离基线已变化，测试输出保留，请先核对。',
  'task termination or admission release requires reconciliation': '无法确认任务结束或任务槽释放，请先核对执行记录。',
  'approved Python test dependency is not prepared': '准备已批准的 Python 测试依赖后，可运行完整基线。',
  'dependency preparation is unknown; reconcile the retained operation': '依赖准备曾中断，记录已保留；请先核对，避免重复下载。',
  'approved Python dependency preparation failed; retained operation requires reconciliation': '依赖来源、内容或准备过程未通过核验；原请求保留，需要核对。',
  'prepared dependency differs from its retained identity': '依赖文件与原记录不符，执行已阻断；请先核对。',
  'dependency admission release requires reconciliation': '无法确认依赖准备的任务槽释放，请先核对记录。',
  'repository or task baseline changed; reconcile dependency preparation': '原仓库或隔离基线已变化，依赖准备结果保留，请先核对。',
};

export function TaskWorkspacePanel({ sessionId, onChanged }: { sessionId: string; onChanged: () => Promise<void> }) {
  const [view, setView] = useState<TaskWorkspace | null>(null);
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState(false);
  const [proposing, setProposing] = useState(false);
  const [error, setError] = useState('');
  const [expanded, setExpanded] = useState(false);
  const [fileLimit, setFileLimit] = useState(200);
  const request = useRef<AbortController | null>(null);
  const requestId = useRef('');
  const testRequestId = useRef('');
  const dependencyRequestId = useRef('');
  const proposalRequestId = useRef('');
  const refresh = useCallback(async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    const timeout = window.setTimeout(() => controller.abort(), 20_000);
    setBusy(true); setError(''); setView(null);
    try {
      const data = await api.getTaskWorkspace(sessionId, controller.signal);
      if (data.proposal && data.proposal.sessionId !== sessionId) throw new Error('proposal session mismatch');
      if (!controller.signal.aborted && request.current === controller) setView(data);
    } catch {
      if (request.current === controller) setError('无法核验工作区，请恢复服务后重新检查。');
    } finally {
      window.clearTimeout(timeout);
      if (request.current === controller) setBusy(false);
    }
  }, [sessionId]);
  useEffect(() => {
    void refresh();
    return () => { request.current?.abort(); request.current = null; };
  }, [refresh]);

  const prepare = async () => {
    if (!view?.available || view.state !== 'unprepared' || busy) return;
    const controller = new AbortController();
    request.current?.abort(); request.current = controller;
    const timeout = window.setTimeout(() => controller.abort(), 120_000);
    const storageKey = `task-workspace-request:${sessionId}`;
    try {
      if (!requestId.current) {
        try { requestId.current = localStorage.getItem(storageKey) || ''; } catch { /* memory fallback; server still prevents duplicate preparation */ }
        requestId.current ||= createContinuationRequestId();
        try { localStorage.setItem(storageKey, requestId.current); } catch { /* same lease remains on the Ledger */ }
      }
      setBusy(true); setError('');
      const data = await api.prepareTaskWorkspace(sessionId, view.eventCount, requestId.current, controller.signal);
      if (!controller.signal.aborted && request.current === controller) { setView(data); await onChanged(); }
    } catch (cause) {
      if (request.current === controller) {
        setView(null);
        setError(cause instanceof ApiError && cause.status === 409 ? '会话已变化，请重新检查；已有副本不会被覆盖。' : '无法确认准备结果，请重新检查；不要另建请求重试。');
      }
    } finally {
      window.clearTimeout(timeout);
      if (request.current === controller) setBusy(false);
    }
  };

  const runBaseline = async () => {
    if (!view?.testAvailable || view.state !== 'prepared' || view.test || busy) return;
    const controller = new AbortController();
    request.current?.abort(); request.current = controller;
    const timeout = window.setTimeout(() => controller.abort(), 180_000);
    const storageKey = `task-baseline-request:${sessionId}`;
    try {
      try { testRequestId.current = localStorage.getItem(storageKey) || testRequestId.current; } catch { /* Ledger prevents a second execution. */ }
      testRequestId.current ||= createContinuationRequestId();
      try { localStorage.setItem(storageKey, testRequestId.current); } catch { /* Ledger retains the operation after a browser restart. */ }
      setBusy(true); setTesting(true); setError('');
      const data = await api.runTaskBaseline(sessionId, view.eventCount, testRequestId.current, controller.signal);
      if (!controller.signal.aborted && request.current === controller) { setView(data); await onChanged(); }
    } catch (cause) {
      if (request.current === controller) {
        setView(null);
        setError(cause instanceof ApiError && cause.status === 409 ? '会话或测试前提已变化，请重新检查。' : controller.signal.reason === 'operator-cancelled' ? '取消请求已发出，请重新检查停止状态；原请求和执行记录会保留。' : '无法确认测试结果，请重新检查；原请求和执行记录会保留。');
      }
    } finally {
      window.clearTimeout(timeout);
      if (request.current === controller) { setBusy(false); setTesting(false); }
    }
  };

  const prepareDependency = async () => {
    if (!view?.dependencyAvailable || busy) return;
    const controller = new AbortController();
    request.current?.abort(); request.current = controller;
    const timeout = window.setTimeout(() => controller.abort(), 90_000);
    const storageKey = `task-dependency-request:${sessionId}`;
    try {
      try { dependencyRequestId.current = localStorage.getItem(storageKey) || dependencyRequestId.current; } catch { /* Ledger keeps the preparation identity. */ }
      dependencyRequestId.current ||= createContinuationRequestId();
      try { localStorage.setItem(storageKey, dependencyRequestId.current); } catch { /* Ledger prevents a repeated download. */ }
      setBusy(true); setError('');
      const data = await api.prepareTaskDependency(sessionId, view.eventCount, dependencyRequestId.current, controller.signal);
      if (!controller.signal.aborted && request.current === controller) { setView(data); await onChanged(); }
    } catch {
      if (request.current === controller) { setView(null); setError('无法确认依赖准备结果，请重新检查；不要另建请求重试。'); }
    } finally {
      window.clearTimeout(timeout);
      if (request.current === controller) setBusy(false);
    }
  };

  const state = busy ? 'unknown' : view?.state || 'unknown';
  const generateProposal = async () => {
    if (!view?.proposalAvailable || busy || view.proposal && (!view.proposal.test.stopped || !['stale', 'failed', 'blocked'].includes(view.proposal.state))) return;
    const controller = new AbortController();
    request.current?.abort(); request.current = controller;
    const timeout = window.setTimeout(() => controller.abort(), 180_000);
    const storageKey = `task-proposal-request:${sessionId}`;
    try {
      if (view.proposal) proposalRequestId.current = createContinuationRequestId();
      else try { proposalRequestId.current = localStorage.getItem(storageKey) || proposalRequestId.current; } catch { /* Ledger retains the proposal request. */ }
      proposalRequestId.current ||= createContinuationRequestId();
      try { localStorage.setItem(storageKey, proposalRequestId.current); } catch { /* Same request remains in memory. */ }
      setBusy(true); setProposing(true); setError('');
      const data = await api.generateTaskProposal(sessionId, view.eventCount, proposalRequestId.current, controller.signal);
      if (data.proposal && data.proposal.sessionId !== sessionId) throw new Error('proposal session mismatch');
      if (!controller.signal.aborted && request.current === controller) { setView(data); await onChanged(); }
    } catch {
      if (request.current === controller) {
        setView(null);
        setError('无法确认提案结果，请重新检查；原请求和执行记录保留，避免重复运行。');
      }
    } finally {
      window.clearTimeout(timeout);
      if (request.current === controller) { setBusy(false); setProposing(false); }
    }
  };
  const proposal = view?.proposal;
  const decideProposal = async (decision: 'approve' | 'reject') => {
    if (!view?.available || !proposal || view.application || busy) return;
    const controller = new AbortController();
    request.current?.abort(); request.current = controller;
    const timeout = window.setTimeout(() => controller.abort(), 30_000);
    const storageKey = `task-application-request:${sessionId}:${proposal.id}:${decision}`;
    let identity = '';
    try {
      try { identity = localStorage.getItem(storageKey) || ''; } catch { /* The Ledger prevents a second application. */ }
      identity ||= createContinuationRequestId();
      try { localStorage.setItem(storageKey, identity); } catch { /* Proposal identity still prevents a second write. */ }
      setBusy(true); setError('');
      const data = await api.decideTaskProposal(sessionId, view.eventCount, identity, proposal.id, decision, controller.signal);
      if (data.proposal?.sessionId !== sessionId || data.application?.proposalId !== proposal.id) throw new Error('application binding mismatch');
      if (!controller.signal.aborted && request.current === controller) { setView(data); await onChanged(); }
    } catch (cause) {
      if (request.current === controller) {
        setView(null);
        setError(cause instanceof ApiError && cause.status === 409 ? '原基线、授权或会话已变化，请重新检查；不会覆盖现有内容。' : '无法确认应用结果，请重新检查；原请求已保留，不会自动重试写入。');
      }
    } finally {
      window.clearTimeout(timeout);
      if (request.current === controller) setBusy(false);
    }
  };
  const boundTest = proposal && /^[a-f0-9]{64}$/.test(proposal.test.inputSHA256)
    && proposal.test.inputSHA256 === proposal.finalStateSHA256 && /^[a-f0-9]{64}$/.test(proposal.test.archiveSHA256)
    && proposal.test.modeTransform === 'readonly-v1' && proposal.test.stopped === true && !proposal.test.truncated;
  const proposalTestState = !proposal ? 'unknown' : !boundTest ? 'unknown'
    : proposal.state === 'ready' && proposal.test.state === 'passed' && proposal.test.exitCode === 0 ? 'passed'
    : proposal.test.state === 'failed' && proposal.test.exitCode !== undefined && proposal.test.exitCode !== 0 ? 'failed'
    : proposal.test.state === 'blocked' ? 'blocked' : 'unknown';
  return <section className="task-workspace-panel" aria-label="隔离工作区">
    <div className="task-workspace-heading"><h2>隔离工作区</h2><span className={`capability-state ${state === 'prepared' ? 'ready' : state === 'unprepared' ? 'unknown' : state}`}>{busy ? '核验中' : labels[state]}</span></div>
    <p className="workspace-description">{view?.reason ? reasons[view.reason] || view.reason : view?.state === 'prepared' ? '当前工作副本已复制，原仓库和已有改动保留。' : '将允许的当前文件作为任务起点。'}</p>
    {view && <dl className="task-workspace-facts">
      <div><dt>仓库</dt><dd>{view.repository}</dd></div>
      {view.workspaceId && <div><dt>隔离标识</dt><dd title={view.workspaceId}>{view.workspaceId.slice(0, 18)}…</dd></div>}
      {view.baseline && <>
        <div><dt>基线</dt><dd title={view.baseline.checksum}>{view.baseline.checksum.slice(0, 12)}</dd></div>
        <div><dt>提交</dt><dd title={view.baseline.head_commit}>{view.baseline.head_commit.slice(0, 8)}</dd></div>
      </>}
    </dl>}
    {view?.baseline && <details onToggle={event => setExpanded(event.currentTarget.open)}>
      <summary>文件清单 · {view.baseline.files.length} 项 · 排除 {view.baseline.excluded.length} 项</summary>
      {expanded && <>
        <ul className="task-workspace-files">{view.baseline.files.slice(0, fileLimit).map(file => <li key={file.path}><code title={file.sha256}>{file.path}</code><span>{file.exists ? `${file.size.toLocaleString()} B` : '已删除'}</span></li>)}</ul>
        {view.baseline.files.length > fileLimit && <button className="subtle-btn" type="button" onClick={() => setFileLimit(value => value + 200)}>显示更多文件</button>}
        <p className="workspace-description">排除：{view.baseline.excluded.map(item => item.path).join('、') || '无'}</p>
      </>}
    </details>}
    {error && <p className="capability-error" role="status">{error}</p>}
    <section className="task-baseline-card" aria-label="基线测试">
      <div className="task-workspace-heading"><h3>基线测试</h3><span role="status">{testing ? '正在运行' : view?.test ? { passed: '通过', failed: '失败', blocked: '需处理', unknown: '待核验' }[view.test.state] : error ? '待核验' : '尚未运行'}</span></div>
      {testing && <button className="subtle-btn" type="button" onClick={() => request.current?.abort('operator-cancelled')}>取消测试</button>}
      {view?.dependency && <div aria-label="Python 测试依赖">
        <p>pytz {view.dependency.version} · {view.dependency.license} · {{ ready: '已准备', blocked: '需处理', unknown: '待核验' }[view.dependency.state]}</p>
        <p><a href={view.dependency.source} target="_blank" rel="noreferrer">批准来源</a> · {view.dependency.size.toLocaleString()} B</p>
        <p title={view.dependency.sha256}>SHA-256：{view.dependency.sha256.slice(0, 16)}…</p>
        {view.dependency.reason && <p className="workspace-description">{reasons[view.dependency.reason] || view.dependency.reason}</p>}
      </div>}
      {view?.dependencyAvailable && <button className="primary-btn" type="button" disabled={busy} onClick={() => void prepareDependency()}>准备 Python 测试依赖</button>}
      {view?.test ? <>
        <p><code>{view.test.command.join(' ')}</code></p>
        <p>退出码：{view.test.exitCode ?? '待核验'}{view.test.truncated ? ' · 输出已截断' : ' · 输出未截断'}</p>
        {(view.test.stopped || view.test.state === 'unknown') && <p>{view.test.stopped ? '隔离任务停止已确认。' : '隔离任务停止尚未确认，请先核对。'}</p>}
        {view.test.reason && <p className="workspace-description">{reasons[view.test.reason] || view.test.reason}</p>}
        <pre className="task-baseline-output" tabIndex={0} aria-label="测试输出">{view.test.output || '暂无可确认的输出。'}</pre>
      </> : <>
        <p className="workspace-description">{view?.testReason ? reasons[view.testReason] || view.testReason : `在已准备的隔离工作区运行完整 ${view?.toolchain === 'python' ? 'Python' : 'Go'} 基线。`}</p>
        {view?.testAvailable && <button className="primary-btn" type="button" disabled={busy || view.state !== 'prepared'} onClick={() => void runBaseline()}>运行 {view.toolchain === 'python' ? 'Python' : 'Go'} 基线</button>}
      </>}
    </section>
    <section className="task-baseline-card" aria-label="代码修改提案">
      <div className="task-workspace-heading"><h3>代码修改提案</h3><span role="status">{proposing ? '正在生成并测试' : proposal ? { ready: '可审查', failed: '测试失败', blocked: '需处理', unknown: '待核验', stale: '已变化，需核验' }[proposal.state] : '尚未生成'}</span></div>
      {proposal ? <>
        <dl className="task-workspace-facts">
          <div><dt>提案</dt><dd>{proposal.id}</dd></div>
          <div><dt>隔离标识</dt><dd>{proposal.workspaceId}</dd></div>
          <div><dt>原基线</dt><dd>{proposal.baselineChecksum}</dd></div>
          <div><dt>来源事件</dt><dd>{proposal.sourceEventChecksum}</dd></div>
          <div><dt>最终状态 SHA-256</dt><dd>{proposal.finalStateSHA256}</dd></div>
        </dl>
        {proposal.files.map(file => <details key={file.path}>
          <summary>{file.path}</summary>
          <p className="workspace-description">{file.beforeExists ? `原 SHA-256：${file.beforeSha256} · 模式 ${file.beforeMode.toString(8)}` : '原文件不存在'}<br />{file.afterExists ? `修改后 SHA-256：${file.afterSha256} · 模式 ${file.afterMode.toString(8)}` : '修改后已删除'}</p>
          <pre className="task-baseline-output" tabIndex={0} aria-label={`${file.path} 修改 diff`}>{file.diff}</pre>
        </details>)}
        <p>修改后测试：<span role="status">{{ passed: '通过', failed: '失败', blocked: '需处理', unknown: '待核验' }[proposalTestState]}</span></p>
        {!boundTest && <p className="workspace-description">测试与最终状态的绑定或停止状态尚未确认；请先核验，不能作为通过证据。</p>}
        <p><code>{proposal.test.command.join(' ')}</code></p>
        <p>退出码：{proposal.test.exitCode ?? '待核验'}{proposal.test.truncated ? ' · 输出已截断' : ' · 输出未截断'}</p>
        <p className="workspace-description">测试输入：{proposal.test.inputSHA256 || '待核验'}</p>
        <p className="workspace-description">归档 SHA-256：{proposal.test.archiveSHA256 || '待核验'}</p>
        <p className="workspace-description">文件模式变换：{proposal.test.modeTransform || '待核验'}</p>
        {proposal.test.reason && <p className="workspace-description">{reasons[proposal.test.reason] || proposal.test.reason}</p>}
        <pre className="task-baseline-output" tabIndex={0} aria-label="提案测试输出">{proposal.test.output || '暂无可确认的输出。'}</pre>
        {view?.application ? <p role="status">{{ applied: '已应用，原 index 保留', rejected: '已拒绝，隔离结果保留', conflict: '应用冲突，现有内容保留', unknown: '应用结果待核验，请先对账' }[view.application.state]}</p> : view?.available && proposal.test.stopped && <div className="task-workspace-actions">
          {proposalTestState === 'passed' && proposal.state === 'ready' && proposal.files.length === 1 && proposal.files[0].beforeExists && proposal.files[0].afterExists && proposal.files[0].beforeMode === proposal.files[0].afterMode && <button type="button" className="primary-btn" disabled={busy} onClick={() => void decideProposal('approve')}>确认应用此文件</button>}
          <button type="button" disabled={busy} onClick={() => void decideProposal('reject')}>拒绝此提案</button>
        </div>}
      </> : <p className="workspace-description">{view?.proposalReason ? reasons[view.proposalReason] || view.proposalReason : '任务完成后，冻结隔离修改并测试；原仓库保留。'}</p>}
      {view?.proposalAvailable && (!proposal || proposal.test.stopped && ['stale', 'failed', 'blocked'].includes(proposal.state)) && <button className="primary-btn" type="button" disabled={busy} onClick={() => void generateProposal()}>{proposal ? '重新生成并测试提案' : '生成并测试提案'}</button>}
    </section>
    <div className="task-workspace-actions">
      {view?.available && view.state === 'unprepared' && <button className="primary-btn" type="button" disabled={busy} onClick={() => void prepare()}>准备隔离工作区</button>}
      <button className="subtle-btn" type="button" disabled={busy} onClick={() => void refresh()}>重新检查工作区</button>
    </div>
  </section>;
}
