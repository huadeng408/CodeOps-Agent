import { useEffect, useState } from 'react';
import type { Session, SessionCheckpoint, SessionEvent } from './types';
import { api, ApiError } from './api';

interface CheckpointPanelProps {
  session: Session;
  refreshKey: number;
  onChanged: () => Promise<void>;
}

export function CheckpointPanel({ session, refreshKey, onChanged }: CheckpointPanelProps) {
  const [checkpoints, setCheckpoints] = useState<SessionCheckpoint[]>([]);
  const [events, setEvents] = useState<SessionEvent[]>([]);
  const [isCreating, setIsCreating] = useState(false);
  const [newLabel, setNewLabel] = useState('');
  const [selectedEventId, setSelectedEventId] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const load = async () => {
    setError('');
    try {
      const [checkpointData, eventData] = await Promise.all([
        api.listCheckpoints(session.id),
        api.listEvents(session.id, { limit: 256 }),
      ]);
      setCheckpoints(checkpointData);
      setEvents(eventData.filter((event) => event.type !== 'session/deleted'));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '加载失败');
    }
  };

  useEffect(() => { void load(); }, [session.id, refreshKey]);

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

  return (
    <section className="checkpoint-panel" aria-label="Checkpoints">
      <div className="panel-heading">
        <h3>Checkpoints</h3>
        <button className="subtle-btn" type="button" onClick={() => setIsCreating((value) => !value)} disabled={busy}>
          {isCreating ? '取消' : '新建'}
        </button>
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
      {checkpoints.length === 0 ? (
        <div className="empty-panel">暂无检查点</div>
      ) : (
        <div className="checkpoint-list">
          {checkpoints.map((checkpoint) => (
            <div className="checkpoint-item" key={checkpoint.id}>
              <div className="checkpoint-title">{checkpoint.label}</div>
              <div className="checkpoint-meta">事件 #{checkpoint.seq} · {new Date(checkpoint.createdAt).toLocaleString()}</div>
              <button className="subtle-btn" type="button" onClick={() => void handleRestore(checkpoint)} disabled={busy}>
                恢复
              </button>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
