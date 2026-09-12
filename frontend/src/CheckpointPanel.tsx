import { useEffect, useRef, useState } from 'react';
import type { ContinuationRuntimeStatus, RecoveryManifest, Session, SessionCheckpoint, SessionEvent, SessionRun, WorkspaceManifest } from './types';
import { api, ApiError } from './api';
import { createContinuationRequestId } from './continuationRequest';
import { isSessionSurfaceEvent } from './compactionPresentation';

function readLocalStorage(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeLocalStorage(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Canonical continuation state lives in the ledger; storage is only a hint.
  }
}

function removeLocalStorage(key: string): void {
  try {
    localStorage.removeItem(key);
  } catch {
    // Best effort only; a new request id remains safe without browser storage.
  }
}

interface CheckpointPanelProps {
  session: Session;
  refreshKey: number;
  onChanged: () => Promise<void>;
  onRunChanged?: (run: SessionRun) => void;
  continuationHealth: ContinuationRuntimeStatus | null;
  continuationHealthKnown: boolean;
}

export function CheckpointPanel({ session, refreshKey, onChanged, onRunChanged, continuationHealth, continuationHealthKnown }: CheckpointPanelProps) {
  const [checkpoints, setCheckpoints] = useState<SessionCheckpoint[]>([]);
  const [manifest, setManifest] = useState<RecoveryManifest | null>(null);
  const [workspace, setWorkspace] = useState<WorkspaceManifest | null>(null);
  const [runHistory, setRunHistory] = useState<SessionRun[]>([]);
  const [runFilter, setRunFilter] = useState<'all' | 'active' | 'failed' | 'completed'>('all');
  const [events, setEvents] = useState<SessionEvent[]>([]);
  const [isCreating, setIsCreating] = useState(false);
  const [newLabel, setNewLabel] = useState('');
  const [selectedEventId, setSelectedEventId] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const requestIds = useRef(new Map<string, string>());
  const loadVersionRef = useRef(0);
  const continuationInFlightRef = useRef(false);

  const load = async () => {
    const loadVersion = ++loadVersionRef.current;
    const loadingSessionID = session.id;
    setError('');
    try {
      const [checkpointData, eventData] = await Promise.all([
        api.listCheckpoints(session.id),
        api.listEvents(session.id),
      ]);
      const [recoveryManifest, workspaceManifest, runs] = await Promise.all([
        api.getRecoveryManifest(session.id).catch(() => null),
        api.getWorkspaceManifest(session.id).catch(() => ({ available: false, reason: 'workspace recovery is unavailable' })),
        api.getRunHistory(session.id).catch(() => []),
      ]);
      if (loadVersion !== loadVersionRef.current || loadingSessionID !== session.id) return;
      setCheckpoints(checkpointData);
      setManifest(recoveryManifest);
      setWorkspace(workspaceManifest);
      setRunHistory(runs);
      const marker = [...eventData].reverse().find((event) => event.rewindTargetSeq !== undefined || event.continuation !== undefined);
      const targetSeq = marker?.continuation?.targetSeq ?? marker?.rewindTargetSeq;
      setEvents(eventData.filter((event) => {
        const recoveryEvent = event.type === 'session/rewind' || event.type === 'session/continued';
        const surfaceEvent = isSessionSurfaceEvent(event);
        return recoveryEvent || (surfaceEvent && (!marker || targetSeq === undefined || event.seq <= targetSeq || event.seq >= marker.seq));
      }));
    } catch (cause) {
      if (loadVersion !== loadVersionRef.current || loadingSessionID !== session.id) return;
      setError(cause instanceof Error ? cause.message : '加载失败');
    }
  };

  useEffect(() => {
    loadVersionRef.current += 1;
    void load();
  }, [session.id, refreshKey]);

  // A checkpoint form belongs to one session. Reset its draft when the user
  // switches sessions so an event id from the previous ledger cannot be sent
  // to the newly selected session.
  useEffect(() => {
    setIsCreating(false);
    setNewLabel('');
    setSelectedEventId('');
    setError('');
    setManifest(null);
    setWorkspace(null);
    setRunHistory([]);
    setRunFilter('all');
  }, [session.id]);

  useEffect(() => {
    const historyHasActiveRun = runHistory.some((run) => run.status === 'queued' || run.status === 'running');
    const sessionHasActiveRun = session.run?.status === 'queued' || session.run?.status === 'running';
    if (!sessionHasActiveRun && !historyHasActiveRun) return undefined;
    const timer = window.setInterval(() => { void onChanged(); }, 1000);
    return () => window.clearInterval(timer);
  }, [session.id, session.run?.runId, session.run?.status, runHistory, onChanged]);

  useEffect(() => {
    if (session.run?.status !== 'failed') return;
    const requestKey = `${session.id}:${session.run.checkpointHash}`;
    requestIds.current.delete(requestKey);
    removeLocalStorage(`continuation-request:${requestKey}`);
  }, [session.id, session.run?.checkpointHash, session.run?.status]);

  const rewindEvents = events.filter((event) => event.type === 'session/rewind' && event.rewindTargetSeq !== undefined);
  const continuationEvents = events.filter((event) => event.type === 'session/continued' && event.continuation);
  const latestRecovery = [...events].reverse().find((event) => event.type === 'session/rewind' || event.type === 'session/continued');
  const latestRecoveryType = manifest?.latestRecovery?.type ?? latestRecovery?.type;
  const visibleRunHistory = runHistory.filter((run) => {
    if (runFilter === 'active') return run.status === 'queued' || run.status === 'running';
    if (runFilter === 'failed') return run.status === 'failed';
    if (runFilter === 'completed') return run.status === 'completed';
    return true;
  });

  const handleExportManifest = () => {
    if (!manifest) return;
    const payload = JSON.stringify({
      ...manifest,
      runs: runHistory,
      workspace: workspace || { available: false, reason: 'workspace recovery is unavailable' },
      exportedAt: new Date().toISOString(),
    }, null, 2);
    const url = URL.createObjectURL(new Blob([payload], { type: 'application/json' }));
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `recovery-manifest-${session.id}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  const handleCreateCheckpoint = async () => {
    if (!newLabel.trim() || !selectedEventId || busy) return;
    setBusy(true);
    setError('');
    try {
      await api.createCheckpoint(session.id, selectedEventId, newLabel.trim(), session.eventCount);
      setNewLabel('');
      setSelectedEventId('');
      setIsCreating(false);
      await onChanged();
    } catch (cause) {
      setError(cause instanceof ApiError && cause.status === 409
        ? '会话已更新，请刷新后重试'
        : cause instanceof Error ? cause.message : '创建失败');
      await onChanged();
    } finally {
      setBusy(false);
    }
  };

  const handleRestore = async (checkpoint: SessionCheckpoint) => {
    if (!window.confirm('恢复后会追加一条恢复记录，原始事件不会删除。继续吗？') || busy) return;
    setBusy(true);
    setError('');
    try {
      await api.restoreCheckpoint(session.id, checkpoint.hash, session.eventCount);
      await onChanged();
    } catch (cause) {
      setError(cause instanceof ApiError && cause.status === 409
        ? '会话已更新，请刷新后重试'
        : cause instanceof Error ? cause.message : '恢复失败');
      await onChanged();
    } finally {
      setBusy(false);
    }
  };

  const handleRestoreWorkspace = async (checkpoint: SessionCheckpoint, worktreeName: string) => {
    if (!window.confirm('将此检查点对应运行的文件恢复到绑定工作区，当前文件必须仍匹配运行后的内容。继续吗？') || busy) return;
    const run = runHistory.find((item) => item.checkpointHash === checkpoint.hash && item.status === 'completed');
    if (!run) {
      setError('找不到该检查点对应的已完成运行记录');
      return;
    }
    setBusy(true);
    setError('');
    try {
      await api.restoreWorkspace(session.id, checkpoint.hash, run.runId, worktreeName, session.eventCount);
      await onChanged();
    } catch (cause) {
      setError(cause instanceof ApiError && cause.status === 409
        ? '工作区已变化或会话已更新，请刷新后重试'
        : cause instanceof Error ? cause.message : '工作区恢复失败');
      await onChanged();
    } finally {
      setBusy(false);
    }
  };

  const handleContinue = async (checkpoint: SessionCheckpoint, forceNewRequestId = false) => {
    if (busy || continuationInFlightRef.current || session.status !== 'paused' || !continuationHealthKnown || continuationHealth?.attached !== true) return;
    continuationInFlightRef.current = true;
    setBusy(true);
    setError('');
    const requestKey = `${session.id}:${checkpoint.hash}`;
    const storageKey = `continuation-request:${requestKey}`;
    let requestId = forceNewRequestId ? '' : requestIds.current.get(requestKey) || readLocalStorage(storageKey) || '';
    if (!requestId) {
      requestId = createContinuationRequestId();
      requestIds.current.set(requestKey, requestId);
      writeLocalStorage(storageKey, requestId);
    }
    try {
      await api.continueSession(session.id, session.eventCount, checkpoint.hash, requestId);
    } catch (cause) {
      setError(cause instanceof ApiError && cause.status === 409
        ? '会话已更新，请刷新后重试'
        : cause instanceof Error ? cause.message : '继续失败');
      await onChanged().catch(() => undefined);
      continuationInFlightRef.current = false;
      setBusy(false);
      return;
    }
    try {
      await onChanged();
    } catch (cause) {
      setError(cause instanceof Error ? `继续请求已受理，刷新状态失败：${cause.message}` : '继续请求已受理，刷新状态失败');
    } finally {
      continuationInFlightRef.current = false;
      setBusy(false);
    }
  };

  const handleRetryRun = async (run: SessionRun) => {
    if (run.status !== 'failed' || busy || session.status !== 'paused') return;
    const checkpoint = checkpoints.find((item) => item.hash === run.checkpointHash);
    if (!checkpoint) {
      setError('找不到该运行对应的检查点，请刷新恢复清单');
      return;
    }
    await handleContinue(checkpoint, true);
  };

  const handleRefreshRun = async (run: SessionRun) => {
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      const fresh = await api.getRun(session.id, run.runId);
      setRunHistory((items) => items.map((item) => item.runId === fresh.runId ? fresh : item));
      onRunChanged?.(fresh);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '运行状态刷新失败');
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="checkpoint-panel" aria-label="Checkpoints">
      <div className="panel-heading">
        <h3>Checkpoints</h3>
        <div className="panel-actions">
          <button className="icon-btn" type="button" title="刷新恢复状态" aria-label="刷新恢复状态" onClick={() => void onChanged()} disabled={busy}>↻</button>
          <button className="icon-btn" type="button" title="导出恢复清单" aria-label="导出恢复清单" onClick={handleExportManifest} disabled={busy || !manifest}>⇩</button>
          <button className="subtle-btn" type="button" onClick={() => setIsCreating((value) => !value)} disabled={busy}>
            {isCreating ? '取消' : '新建'}
          </button>
        </div>
      </div>
      {isCreating && (
        <div className="checkpoint-form">
          <input
            aria-label="检查点标签"
            value={newLabel}
            onChange={(event) => setNewLabel(event.target.value)}
            placeholder="检查点标签"
          />
          <select aria-label="选择事件" value={selectedEventId} onChange={(event) => setSelectedEventId(event.target.value)}>
            <option value="">选择事件</option>
            {events.map((event) => (
              <option key={event.id} value={event.id}>
                #{event.seq} {event.content?.slice(0, 38) || event.type}
              </option>
            ))}
          </select>
          <button className="primary-btn" type="button" onClick={handleCreateCheckpoint} disabled={busy || !newLabel.trim() || !selectedEventId}>
            {busy ? '保存中...' : '保存'}
          </button>
        </div>
      )}
      {error && <div className="inline-error" role="alert">{error}</div>}
      {(manifest || rewindEvents.length > 0 || continuationEvents.length > 0) && (
        <div className="recovery-summary" aria-label="恢复历史摘要">
          <div className="recovery-summary-title">恢复历史</div>
          <div className="recovery-summary-meta">ledger #{manifest?.ledgerSeq ?? '-'} · 事件 {manifest?.eventCount ?? events.length}</div>
          {manifest?.latestLedgerHash && <div className="recovery-summary-meta mono-value">链指纹 {manifest.latestLedgerHash.slice(0, 12)}…</div>}
          <div className="recovery-summary-meta">恢复 {manifest?.rewindCount ?? rewindEvents.length} 次 · 继续 {manifest?.continuationCount ?? continuationEvents.length} 次</div>
          {manifest?.currentRun?.retryOfRunId && <div className="recovery-summary-meta">当前运行承接 {manifest.currentRun.retryOfRunId.slice(0, 12)}… · 历史失败 {manifest.currentRun.retryOfRunIds?.length ?? 1} 个</div>}
          {runHistory.length > 1 && <div className="recovery-summary-meta">运行历史 {runHistory.length} 次：{runHistory.map((run) => `${run.runId.slice(0, 8)}…/${run.status}`).join(' · ')}</div>}
          {latestRecoveryType === 'session/rewind' && <div className="recovery-summary-meta">最近恢复到事件 #{manifest?.latestRecovery?.targetSeq ?? latestRecovery?.rewindTargetSeq}</div>}
          {latestRecoveryType === 'session/continued' && <div className="recovery-summary-meta">最近从事件 #{manifest?.latestRecovery?.targetSeq ?? latestRecovery?.continuation?.targetSeq} 继续（第 {manifest?.latestRecovery?.resumeCount ?? latestRecovery?.continuation?.resumeCount} 次）</div>}
        </div>
      )}
      {!manifest && <div className="recovery-summary" role="status"><div className="recovery-summary-title">恢复清单暂不可用</div><div className="recovery-summary-meta">已显示 canonical ledger 事件与检查点，可稍后刷新恢复状态。</div></div>}
      {runHistory.length > 0 && (
        <div className="run-history-list" aria-label="运行历史详情">
          <div className="run-history-heading"><div className="recovery-summary-title">运行记录</div><select aria-label="运行历史筛选" value={runFilter} onChange={(event) => setRunFilter(event.target.value as typeof runFilter)}><option value="all">全部</option><option value="active">进行中</option><option value="failed">失败</option><option value="completed">已完成</option></select></div>
          {visibleRunHistory.length === 0 ? <div className="recovery-summary-meta">没有匹配的运行记录</div> : visibleRunHistory.map((run) => (
            <div className="run-history-item" key={run.runId}>
              <div className="run-history-main">
                <span className={`run-status ${run.status}`}>{run.status}</span>
                <span className="mono-value">{run.runId.slice(0, 12)}…</span>
                <span>第 {run.attempt || 1} 次</span>
              </div>
              {run.retryOfRunId && <div className="recovery-summary-meta">承接 {run.retryOfRunId.slice(0, 12)}…</div>}
              {run.error && <div className="recovery-summary-meta">{run.error}</div>}
              <button className="subtle-btn" type="button" onClick={() => void handleRefreshRun(run)} disabled={busy}>刷新状态</button>
              {run.status === 'failed' && <button className="subtle-btn" type="button" onClick={() => void handleRetryRun(run)} disabled={busy || session.status !== 'paused' || !continuationHealthKnown || continuationHealth?.attached !== true}>再次运行</button>}
            </div>
          ))}
        </div>
      )}
      {workspace && (
        <div className="workspace-summary" aria-label="工作区恢复摘要">
          <div className="recovery-summary-title">工作区恢复</div>
          {!workspace.available ? (
            <div className="recovery-summary-meta">{workspace.reason || '暂无绑定工作区'}</div>
          ) : workspace.worktrees?.map((tree) => (
            <div className="workspace-item" key={tree.name}>
              <div className="recovery-summary-meta"><strong>{tree.name}</strong> · {tree.status || 'active'}{tree.active ? ' · 当前' : ''}</div>
              {tree.diffError ? <div className="recovery-summary-meta">{tree.diffError}</div> : <div className="recovery-summary-meta">{tree.diffLines?.join(' · ') || 'working tree clean'}</div>}
              {checkpoints.some((checkpoint) => runHistory.some((run) => run.checkpointHash === checkpoint.hash && run.status === 'completed')) && (
                <div className="checkpoint-actions">
                  {checkpoints.filter((checkpoint) => runHistory.some((run) => run.checkpointHash === checkpoint.hash && run.status === 'completed')).map((checkpoint) => (
                    <button className="subtle-btn" type="button" key={`${tree.name}:${checkpoint.hash}`} onClick={() => void handleRestoreWorkspace(checkpoint, tree.name)} disabled={busy}>
                      恢复到“{checkpoint.label}”
                    </button>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
      {checkpoints.length === 0 ? (
        <div className="empty-panel">暂无检查点</div>
      ) : (
        <div className="checkpoint-list">
          {checkpoints.map((checkpoint) => (
            <div className="checkpoint-item" key={checkpoint.id}>
              <div className="checkpoint-title">{checkpoint.label}</div>
              <div className="checkpoint-meta">事件 #{checkpoint.seq} · {new Date(checkpoint.createdAt).toLocaleString()}</div>
              <div className="checkpoint-actions">
                <button className="subtle-btn" type="button" onClick={() => void handleRestore(checkpoint)} disabled={busy}>
                  恢复
                </button>
                <button className="subtle-btn" type="button" onClick={() => void handleContinue(checkpoint)} disabled={busy || session.status !== 'paused' || !continuationHealthKnown || continuationHealth?.attached !== true}>
                  {!continuationHealthKnown ? '检查编排器...' : continuationHealth?.attached !== true ? '编排器恢复中...' : session.run?.checkpointHash === checkpoint.hash && (session.run.status === 'running' || session.run.status === 'queued') ? '运行中' : session.run?.checkpointHash === checkpoint.hash && session.run.status === 'failed' ? '重试' : '继续'}
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
