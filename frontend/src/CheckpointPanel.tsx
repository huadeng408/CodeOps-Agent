import { useEffect, useRef, useState } from 'react';
import type { RecoveryManifest, Session, SessionCheckpoint, SessionEvent, WorkspaceManifest } from './types';
import { api, ApiError } from './api';

interface CheckpointPanelProps {
  session: Session;
  refreshKey: number;
  onChanged: () => Promise<void>;
}

export function CheckpointPanel({ session, refreshKey, onChanged }: CheckpointPanelProps) {
  const [checkpoints, setCheckpoints] = useState<SessionCheckpoint[]>([]);
  const [manifest, setManifest] = useState<RecoveryManifest | null>(null);
  const [workspace, setWorkspace] = useState<WorkspaceManifest | null>(null);
  const [events, setEvents] = useState<SessionEvent[]>([]);
  const [isCreating, setIsCreating] = useState(false);
  const [newLabel, setNewLabel] = useState('');
  const [selectedEventId, setSelectedEventId] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const requestIds = useRef(new Map<string, string>());

  const load = async () => {
    setError('');
    try {
      const [checkpointData, eventData, recoveryManifest, workspaceManifest] = await Promise.all([
        api.listCheckpoints(session.id),
        api.listEvents(session.id),
        api.getRecoveryManifest(session.id),
        api.getWorkspaceManifest(session.id),
      ]);
      setCheckpoints(checkpointData);
      setManifest(recoveryManifest);
      setWorkspace(workspaceManifest);
      const marker = [...eventData].reverse().find((event) => event.rewindTargetSeq !== undefined || event.continuation !== undefined);
      const targetSeq = marker?.continuation?.targetSeq ?? marker?.rewindTargetSeq;
      setEvents(eventData.filter((event) => {
        const recoveryEvent = event.type === 'session/rewind' || event.type === 'session/continued';
        const surfaceEvent = ['user/message', 'assistant/message', 'tool/call', 'tool/result'].includes(event.type);
        return recoveryEvent || (surfaceEvent && (!marker || targetSeq === undefined || event.seq <= targetSeq || event.seq >= marker.seq));
      }));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '加载失败');
    }
  };

  useEffect(() => { void load(); }, [session.id, refreshKey]);

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
  }, [session.id]);

  useEffect(() => {
    if (!session.run || (session.run.status !== 'queued' && session.run.status !== 'running')) return undefined;
    const timer = window.setInterval(() => { void onChanged(); }, 1000);
    return () => window.clearInterval(timer);
  }, [session.id, session.run?.runId, session.run?.status, onChanged]);

  useEffect(() => {
    if (session.run?.status !== 'failed') return;
    const requestKey = `${session.id}:${session.run.checkpointHash}`;
    requestIds.current.delete(requestKey);
    localStorage.removeItem(`continuation-request:${requestKey}`);
  }, [session.id, session.run?.checkpointHash, session.run?.status]);

  const rewindEvents = events.filter((event) => event.type === 'session/rewind' && event.rewindTargetSeq !== undefined);
  const continuationEvents = events.filter((event) => event.type === 'session/continued' && event.continuation);
  const latestRecovery = [...events].reverse().find((event) => event.type === 'session/rewind' || event.type === 'session/continued');
  const latestRecoveryType = manifest?.latestRecovery?.type ?? latestRecovery?.type;

  const handleExportManifest = () => {
    if (!manifest) return;
    const payload = JSON.stringify(manifest, null, 2);
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

  const handleContinue = async (checkpoint: SessionCheckpoint) => {
    if (busy || session.status !== 'paused') return;
    setBusy(true);
    setError('');
    const requestKey = `${session.id}:${checkpoint.hash}`;
    const storageKey = `continuation-request:${requestKey}`;
    let requestId = requestIds.current.get(requestKey) || localStorage.getItem(storageKey) || '';
    if (!requestId) {
      requestId = `browser:${crypto.randomUUID()}`;
      requestIds.current.set(requestKey, requestId);
      localStorage.setItem(storageKey, requestId);
    }
    try {
      await api.continueSession(session.id, session.eventCount, checkpoint.hash, requestId);
    } catch (cause) {
      setError(cause instanceof ApiError && cause.status === 409
        ? '会话已更新，请刷新后重试'
        : cause instanceof Error ? cause.message : '继续失败');
      await onChanged().catch(() => undefined);
      setBusy(false);
      return;
    }
    try {
      await onChanged();
    } catch (cause) {
      setError(cause instanceof Error ? `继续请求已受理，刷新状态失败：${cause.message}` : '继续请求已受理，刷新状态失败');
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
          {latestRecoveryType === 'session/rewind' && <div className="recovery-summary-meta">最近恢复到事件 #{manifest?.latestRecovery?.targetSeq ?? latestRecovery?.rewindTargetSeq}</div>}
          {latestRecoveryType === 'session/continued' && <div className="recovery-summary-meta">最近从事件 #{manifest?.latestRecovery?.targetSeq ?? latestRecovery?.continuation?.targetSeq} 继续（第 {manifest?.latestRecovery?.resumeCount ?? latestRecovery?.continuation?.resumeCount} 次）</div>}
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
                <button className="subtle-btn" type="button" onClick={() => void handleContinue(checkpoint)} disabled={busy || session.status !== 'paused'}>
                  {session.run?.checkpointHash === checkpoint.hash && (session.run.status === 'running' || session.run.status === 'queued') ? '运行中' : session.run?.checkpointHash === checkpoint.hash && session.run.status === 'failed' ? '重试' : '继续'}
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
