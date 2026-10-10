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
};

export function TaskWorkspacePanel({ sessionId, onChanged }: { sessionId: string; onChanged: () => Promise<void> }) {
  const [view, setView] = useState<TaskWorkspace | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [expanded, setExpanded] = useState(false);
  const [fileLimit, setFileLimit] = useState(200);
  const request = useRef<AbortController | null>(null);
  const requestId = useRef('');
  const refresh = useCallback(async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    const timeout = window.setTimeout(() => controller.abort(), 20_000);
    setBusy(true); setError(''); setView(null);
    try {
      const data = await api.getTaskWorkspace(sessionId, controller.signal);
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

  const state = busy ? 'unknown' : view?.state || 'unknown';
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
    <div className="task-workspace-actions">
      {view?.available && view.state === 'unprepared' && <button className="primary-btn" type="button" disabled={busy} onClick={() => void prepare()}>准备隔离工作区</button>}
      <button className="subtle-btn" type="button" disabled={busy} onClick={() => void refresh()}>重新检查工作区</button>
    </div>
  </section>;
}
