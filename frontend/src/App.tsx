import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import type { ContinuationRuntimeStatus, Session, SessionEvent } from './types';
import { ApiError, api } from './api';
import { useWebSocket } from './useWebSocket';
import { CheckpointPanel } from './CheckpointPanel';
import { LoginPage } from './LoginPage';
import { createContinuationRequestId } from './continuationRequest';
import { retryPendingMessage, sendFailureMessage } from './sendRetry';
import { compactionPresentation, isCompactionEvent } from './compactionPresentation';
import { mergeSessionEvents } from './eventMerge';

function renderInlineMarkdown(value: string): ReactNode {
  const parts = value.split(/(\*\*[^*]+\*\*|`[^`]+`)/g);
  return parts.map((part, index) => {
    if (part.startsWith('**') && part.endsWith('**')) return <strong key={index}>{part.slice(2, -2)}</strong>;
    if (part.startsWith('`') && part.endsWith('`')) return <code key={index}>{part.slice(1, -1)}</code>;
    return <span key={index}>{part}</span>;
  });
}

function renderMessageMarkdown(value: string): ReactNode {
  const lines = value.split(/\r?\n/);
  const nodes: React.ReactNode[] = [];
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const next = lines[index + 1];
    if (line?.trim().startsWith('|') && next?.trim().match(/^\|?\s*:?-{3,}/)) {
      const headers = line.split('|').slice(1, -1).map((cell) => cell.trim());
      const rows: string[][] = [];
      index += 2;
      while (index < lines.length && lines[index].trim().startsWith('|')) {
        rows.push(lines[index].split('|').slice(1, -1).map((cell) => cell.trim()));
        index += 1;
      }
      nodes.push(<table key={`table-${index}`}><thead><tr>{headers.map((cell) => <th key={cell}>{renderInlineMarkdown(cell)}</th>)}</tr></thead><tbody>{rows.map((row, rowIndex) => <tr key={rowIndex}>{row.map((cell, cellIndex) => <td key={cellIndex}>{renderInlineMarkdown(cell)}</td>)}</tr>)}</tbody></table>);
      index -= 1;
      continue;
    }
    if (line.trim()) nodes.push(<p key={index}>{renderInlineMarkdown(line)}</p>);
  }
  return nodes;
}

function readEventSummary(content: string): string {
  const normalized = content.trim();
  if (!normalized) return '读取文件';
  if (normalized.startsWith('[Read ')) {
    const closing = normalized.indexOf(']');
    if (closing > 0) return normalized.slice(1, closing);
  }
  const firstLine = normalized.split(/\r?\n/, 1)[0] || normalized;
  return firstLine.length > 140 ? firstLine.slice(0, 137) + '…' : firstLine;
}

function renderToolContent(event: SessionEvent): ReactNode {
  const isRead = event.toolName === 'Read' || event.content.trimStart().startsWith('[Read ');
  if (!isRead) return renderMessageMarkdown(event.content || event.type);
  const summary = readEventSummary(event.content);
  const full = event.content.trim();
  return (
    <>
      <div className="tool-summary">{summary}</div>
      {full.length > summary.length && (
        <details className="tool-details">
          <summary>查看完整读取结果</summary>
          <pre>{full}</pre>
        </details>
      )}
    </>
  );
}

function hasSequenceGap(after: number, incoming: SessionEvent[]): boolean {
  if (after < 0 || incoming.length === 0) return false;
  const sorted = [...new Map(incoming.map((event) => [event.seq, event])).values()]
    .filter((event) => event.seq > after)
    .sort((a, b) => a.seq - b.seq);
  let expected = after + 1;
  for (const event of sorted) {
    if (event.seq > expected) return true;
    if (event.seq === expected) expected += 1;
  }
  return false;
}

function persistedSessionCursor(sessionId: string, cursor: number): void {
  try {
    localStorage.setItem(`codeops:ledger-cursor:${sessionId}`, String(cursor));
  } catch {
    // Storage can be disabled in private browsing; ledger recovery remains
    // authoritative and does not depend on this diagnostic hint.
  }
}

function sessionDraftKey(sessionId: string): string {
  return `codeops:draft:${sessionId}`;
}

function sessionMessageRequestKey(sessionId: string): string {
  return `codeops:message-request:${sessionId}`;
}

function messageRequestId(sessionId: string, content: string): string {
  const key = sessionMessageRequestKey(sessionId);
  const stored = readLocalStorage(key);
  if (stored) {
    try {
      const pending = JSON.parse(stored) as { content?: string; requestId?: string };
      if (pending.content === content && pending.requestId) return pending.requestId;
    } catch {
      // Replace malformed browser hints; the Session Ledger remains canonical.
    }
  }
  const requestId = createContinuationRequestId();
  writeLocalStorage(key, JSON.stringify({ content, requestId }));
  return requestId;
}

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
    // Session continuity is ledger-backed; browser storage is only a hint.
  }
}

function removeLocalStorage(key: string): void {
  try {
    localStorage.removeItem(key);
  } catch {
    // Session continuity is ledger-backed; browser storage is only a hint.
  }
}

// The ledger remains the transport cursor and audit history. The active UI
// surface hides only the stale branch between the latest rewind target and its
// marker, so later events can continue to stream without losing locality.
function deriveActiveEvents(events: SessionEvent[]): SessionEvent[] {
	const marker = [...events].reverse().find((event) => event.rewindTargetSeq !== undefined || event.continuation !== undefined);
	const targetSeq = marker?.continuation?.targetSeq ?? marker?.rewindTargetSeq;
	if (!marker || targetSeq === undefined) return events;
	return events.filter((event) => event.seq <= targetSeq || event.seq >= marker.seq);
}

function errorMessage(cause: unknown, fallback: string): string {
  if (cause instanceof ApiError && cause.status === 409) return '会话已被其他操作更新，请重试';
  return cause instanceof Error ? cause.message : fallback;
}

type EventKind = 'messages' | 'tools' | 'approvals' | 'code' | 'progress';

function renderProgressContent(event: SessionEvent): ReactNode {
  const progress = event.progress || compactionPresentation(event);
  if (!progress) return null;
  return (
    <div className={`progress-card ${progress.kind}`} role="status" aria-label="任务阶段性进展">
      <div className="progress-card-title">{progress.title}</div>
      <div className="progress-card-summary">{progress.summary}</div>
    </div>
  );
}

function eventKind(event: SessionEvent): EventKind | 'progress' {
	if (event.progress || event.type === 'session/progress' || isCompactionEvent(event)) return 'progress';
	if (event.type.includes('approval')) return 'approvals';
	if (event.codeModification || event.type.startsWith('code/') || event.type.startsWith('patch/') || event.type.startsWith('workspace/patch')) return 'code';
	if (event.toolName || event.type.startsWith('tool/')) return 'tools';
  return 'messages';
}

function eventKindLabel(kind: EventKind): string {
  return kind === 'tools' ? '工具' : kind === 'approvals' ? '审批' : kind === 'code' ? '修改' : kind === 'progress' ? '进展' : '消息';
}

function App() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [selectedSession, setSelectedSession] = useState<Session | null>(null);
  const [message, setMessage] = useState('');
  const [isDarkTheme, setIsDarkTheme] = useState(() => readLocalStorage('theme') === 'dark');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [pendingRetry, setPendingRetry] = useState<{ sessionId: string; content: string; requestId: string } | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [showCreate, setShowCreate] = useState(false);
  const [newTitle, setNewTitle] = useState('');
  const [newProject, setNewProject] = useState('default-project');
  const [newGoal, setNewGoal] = useState('');
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState('');
  const [statusDraft, setStatusDraft] = useState<Session['status']>('running');
  const [showDetails, setShowDetails] = useState(false);
  const [continuationHealth, setContinuationHealth] = useState<ContinuationRuntimeStatus | null>(null);
  const [continuationHealthKnown, setContinuationHealthKnown] = useState(false);
  const selectionGenerationRef = useRef(0);
  const canonicalSelectionRef = useRef('');
  const continuationInFlightRef = useRef(false);
  const retiredSessionIDsRef = useRef(new Set<string>());
  const messageListRef = useRef<HTMLDivElement>(null);
  const followMessagesRef = useRef(true);

  useEffect(() => {
    const list = messageListRef.current;
    if (!list) return;
    followMessagesRef.current = true;
    list.scrollTop = list.scrollHeight;
    const onScroll = () => { followMessagesRef.current = list.scrollHeight - list.scrollTop - list.clientHeight < 100; };
    const observer = new MutationObserver(() => {
      if (followMessagesRef.current) list.scrollTop = list.scrollHeight;
    });
    observer.observe(list, { childList: true, subtree: true, characterData: true });
    list.addEventListener('scroll', onScroll);
    return () => { observer.disconnect(); list.removeEventListener('scroll', onScroll); };
  }, [loading, selectedSession?.id]);

  useEffect(() => {
    document.body.toggleAttribute('data-ds-dark-theme', isDarkTheme);
    writeLocalStorage('theme', isDarkTheme ? 'dark' : 'light');
  }, [isDarkTheme]);

  useEffect(() => {
    if (selectedSession) setStatusDraft(selectedSession.status);
  }, [selectedSession?.id, selectedSession?.status]);

  useEffect(() => {
    if (!selectedSession) {
      setMessage('');
      return;
    }
    try {
      setMessage(readLocalStorage(sessionDraftKey(selectedSession.id)) || '');
    } catch {
      setMessage('');
    }
  }, [selectedSession?.id]);

  useEffect(() => {
    if (!selectedSession) return;
    try {
      const draft = message.trim();
      if (draft) writeLocalStorage(sessionDraftKey(selectedSession.id), message);
      else removeLocalStorage(sessionDraftKey(selectedSession.id));
    } catch {
      // Draft persistence is a convenience; canonical ledger recovery does
      // not depend on browser storage being available.
    }
  }, [message, selectedSession?.id]);

  const syncSessions = useCallback(async (preferredID?: string) => {
    const data = await api.listSessions();
    setSessions(data);
    setSelectedSession((current) => {
      const remembered = readLocalStorage('codeops:selected-session');
      const wanted = preferredID || current?.id || remembered;
      const next = (wanted && data.find((session) => session.id === wanted)) || data[0] || null;
      if (next) writeLocalStorage('codeops:selected-session', next.id);
      else removeLocalStorage('codeops:selected-session');
      return next;
    });
    return data;
  }, []);

  const refreshSelected = useCallback(async () => {
    if (!selectedSession) {
      await syncSessions();
      return;
    }
    const generation = ++selectionGenerationRef.current;
    const fresh = await api.getSession(selectedSession.id);
    if (generation !== selectionGenerationRef.current) return;
    setSelectedSession(fresh);
    setSessions((items) => items.map((item) => item.id === fresh.id ? fresh : item));
    setRefreshKey((value) => value + 1);
  }, [selectedSession, syncSessions]);

  const handleSelectSession = useCallback(async (session: Session) => {
    setSelectedSession(session);
    writeLocalStorage('codeops:selected-session', session.id);
    setStatusDraft(session.status);
    setEditingTitle(false);
    setError('');
  }, []);

  useEffect(() => {
    const sessionId = selectedSession?.id;
    if (!sessionId || canonicalSelectionRef.current === sessionId) return;
    const controller = new AbortController();
    canonicalSelectionRef.current = sessionId;
    const generation = ++selectionGenerationRef.current;
    // Sidebar/list data is only an index. Re-open from the canonical ledger
    // projection so run status, checkpoint and retry lineage survive reloads.
    void api.getSession(sessionId, controller.signal).then((fresh) => {
      if (generation !== selectionGenerationRef.current) return;
      setSelectedSession(fresh);
      setSessions((items) => items.map((item) => item.id === fresh.id ? fresh : item));
      setStatusDraft(fresh.status);
      setRefreshKey((value) => value + 1);
    }).catch((cause) => {
      if (generation === selectionGenerationRef.current && !retiredSessionIDsRef.current.has(sessionId)) {
        setError(errorMessage(cause, '会话恢复状态加载失败'));
      }
    });
    return () => controller.abort();
  }, [selectedSession?.id]);

  useEffect(() => {
    const sessionId = selectedSession?.id;
    const runStatus = selectedSession?.run?.status;
    const active = selectedSession?.status === 'queued'
      || selectedSession?.status === 'running'
      || runStatus === 'queued'
      || runStatus === 'running';
    if (!sessionId || !active) return undefined;
    let stopped = false;
    const controller = new AbortController();
    let pollGeneration = 0;
    let inFlight = false;
    const refresh = async () => {
      if (stopped || inFlight) return;
      inFlight = true;
      const generation = ++pollGeneration;
      try {
        const fresh = await api.getSession(sessionId, controller.signal);
        if (stopped || generation !== pollGeneration) return;
        setSessions((items) => items.map((item) => item.id === fresh.id ? fresh : item));
        setSelectedSession((current) => {
          if (!current || current.id !== fresh.id) return current;
          const changed = current.status !== fresh.status
            || current.eventCount !== fresh.eventCount
            || current.run?.runId !== fresh.run?.runId
            || current.run?.status !== fresh.run?.status
            || current.run?.attempt !== fresh.run?.attempt;
          if (changed) setRefreshKey((value) => value + 1);
          return fresh;
        });
        setStatusDraft(fresh.status);
      } catch (cause) {
        if (!stopped && generation === pollGeneration && !retiredSessionIDsRef.current.has(sessionId)) {
          setError(errorMessage(cause, '运行状态同步失败'));
        }
      } finally {
        inFlight = false;
      }
    };
    void refresh();
    const timer = window.setInterval(() => { void refresh(); }, 2000);
    return () => {
      stopped = true;
      controller.abort();
      window.clearInterval(timer);
    };
  }, [selectedSession?.id, selectedSession?.status, selectedSession?.run?.status]);

  useEffect(() => {
    void syncSessions().catch((cause) => setError(errorMessage(cause, '会话加载失败'))).finally(() => setLoading(false));
  }, [syncSessions]);

  useEffect(() => {
    let stopped = false;
    const controller = new AbortController();
    let inFlight = false;
    let healthGeneration = 0;
    const refreshHealth = async () => {
      if (stopped || inFlight) return;
      inFlight = true;
      const generation = ++healthGeneration;
      try {
        const health = await api.continuationHealth(controller.signal);
        if (!stopped && generation === healthGeneration) {
          setContinuationHealth(health);
          setContinuationHealthKnown(true);
        }
      } catch {
        if (!stopped && generation === healthGeneration) {
          setContinuationHealth(null);
          setContinuationHealthKnown(false);
        }
      } finally {
        inFlight = false;
      }
    };
    void refreshHealth();
    const timer = window.setInterval(() => { void refreshHealth(); }, 3000);
    return () => { stopped = true; controller.abort(); window.clearInterval(timer); };
  }, []);

  const handleCreateSession = async (event: FormEvent) => {
    event.preventDefault();
    if (!newTitle.trim() || busy) return;
    setBusy(true);
    setError('');
    try {
      const created = await api.createSession(newProject.trim() || 'default-project', newTitle.trim(), newGoal.trim());
      setNewTitle('');
      setNewGoal('');
      setShowCreate(false);
      await syncSessions(created.id);
    } catch (cause) {
      setError(errorMessage(cause, '创建会话失败'));
    } finally {
      setBusy(false);
    }
  };

  const handleRename = async () => {
    if (!selectedSession || !titleDraft.trim() || busy) return;
    setBusy(true);
    setError('');
    try {
      await api.updateSessionTitle(selectedSession.id, titleDraft.trim(), selectedSession.eventCount);
      setEditingTitle(false);
      await refreshSelected();
    } catch (cause) {
      setError(errorMessage(cause, '重命名失败'));
      await refreshSelected().catch(() => undefined);
    } finally {
      setBusy(false);
    }
  };

  const handleStatus = async (status: Session['status']) => {
    if (!selectedSession || status === selectedSession.status || busy) return;
    setBusy(true);
    setError('');
    try {
      await api.updateSessionStatus(selectedSession.id, status, selectedSession.eventCount);
      await refreshSelected();
    } catch (cause) {
      setError(errorMessage(cause, '状态更新失败'));
      await refreshSelected().catch(() => undefined);
    } finally {
      setBusy(false);
    }
  };

  const handleDelete = async () => {
    if (!selectedSession || busy || !window.confirm('删除后会话只会标记删除，事件仍保留。继续吗？')) return;
    const retiringSession = selectedSession;
    const sessionID = retiringSession.id;
    retiredSessionIDsRef.current.add(sessionID);
    selectionGenerationRef.current += 1;
    canonicalSelectionRef.current = '';
    setSelectedSession(null);
    setBusy(true);
    setError('');
    let deleted = false;
    try {
      await api.deleteSession(sessionID, retiringSession.eventCount);
      deleted = true;
      await syncSessions();
      setError('');
    } catch (cause) {
      if (!deleted) {
        retiredSessionIDsRef.current.delete(sessionID);
        setSelectedSession(retiringSession);
        writeLocalStorage('codeops:selected-session', sessionID);
        setError(errorMessage(cause, '删除会话失败'));
      } else {
        setError(errorMessage(cause, '会话列表刷新失败'));
      }
    } finally {
      setBusy(false);
    }
  };

  const handleSendMessage = async (event: FormEvent) => {
    event.preventDefault();
    if (!selectedSession || !message.trim() || busy) return;
    const content = message.trim();
    setBusy(true);
    setError('');
    const requestId = messageRequestId(selectedSession.id, content);
    try {
      await api.submitMessage(selectedSession.id, content, selectedSession.eventCount, requestId);
      setPendingRetry(null);
      setMessage('');
      removeLocalStorage(sessionDraftKey(selectedSession.id));
      removeLocalStorage(sessionMessageRequestKey(selectedSession.id));
      await refreshSelected();
    } catch (cause) {
      setPendingRetry({ sessionId: selectedSession.id, content, requestId });
      setMessage(content);
      setError(sendFailureMessage(cause));
      await refreshSelected().catch(() => undefined);
    } finally {
      setBusy(false);
    }
  };

  const handleRetryMessage = async () => {
    const pending = pendingRetry;
    if (!pending || busy || !selectedSession || selectedSession.id !== pending.sessionId) return;
    setBusy(true);
    setError('');
    try {
      await retryPendingMessage(
        pending,
        async (sessionId) => {
          const fresh = await api.getSession(sessionId);
          setSelectedSession(fresh);
          setSessions((items) => items.map((item) => item.id === fresh.id ? fresh : item));
          setRefreshKey((value) => value + 1);
          return { sessionId: fresh.id, eventCount: fresh.eventCount };
        },
        (request) => api.submitMessage(request.sessionId, request.content, request.expectedSeq, request.requestId),
      );
      setPendingRetry(null);
      setMessage('');
      removeLocalStorage(sessionDraftKey(pending.sessionId));
      removeLocalStorage(sessionMessageRequestKey(pending.sessionId));
      await refreshSelected();
    } catch (cause) {
      setMessage(pending.content);
      setError(sendFailureMessage(cause));
      await refreshSelected().catch(() => undefined);
    } finally {
      setBusy(false);
    }
  };

  const handleContinueSession = async () => {
    if (!selectedSession || busy || continuationInFlightRef.current || selectedSession.status !== 'paused' || !selectedSession.run?.checkpointHash) return;
    if (!continuationHealthKnown || continuationHealth?.attached !== true) {
      setError('编排器正在恢复，暂不能继续任务');
      return;
    }
    continuationInFlightRef.current = true;
    setBusy(true);
    setError('');
    const checkpointHash = selectedSession.run.checkpointHash;
    const storageKey = `continuation-request:${selectedSession.id}:${checkpointHash}`;
    const retryingFailedRun = selectedSession.run.status === 'failed';
    const requestId = retryingFailedRun
      ? createContinuationRequestId()
      : readLocalStorage(storageKey) || createContinuationRequestId();
    writeLocalStorage(storageKey, requestId);
    try {
      await api.continueSession(selectedSession.id, selectedSession.eventCount, checkpointHash, requestId);
      await refreshSelected();
    } catch (cause) {
      setError(errorMessage(cause, '继续历史任务失败'));
      await refreshSelected().catch(() => undefined);
    } finally {
      continuationInFlightRef.current = false;
      setBusy(false);
    }
  };

  const handleLogout = async () => {
    setBusy(true);
    try {
      await api.logout();
      window.location.reload();
    } catch (cause) {
      setError(errorMessage(cause, '退出失败'));
      setBusy(false);
    }
  };

  const sessionsByProject = useMemo(() => sessions.reduce((groups, session) => {
    (groups[session.projectName] ||= []).push(session);
    return groups;
  }, {} as Record<string, Session[]>), [sessions]);

  if (loading) return <div className="loading-screen">正在加载...</div>;

  return (
    <div className={`app-frame ${showDetails ? 'details-open' : ''}`}>
      <aside className="sidebar">
        <div className="sidebar-header">
          <div className="sidebar-brand">CodeOps Agent</div>
          <div className="sidebar-actions">
            <button className="icon-btn" type="button" title="退出登录" aria-label="退出登录" onClick={() => void handleLogout()} disabled={busy}>↪</button>
            <button className="new-session-btn" type="button" onClick={() => setShowCreate((value) => !value)} disabled={busy}>+ 新建</button>
          </div>
        </div>
        {showCreate && (
          <form className="create-session-form" onSubmit={(event) => void handleCreateSession(event)}>
            <input aria-label="项目" value={newProject} onChange={(event) => setNewProject(event.target.value)} placeholder="项目" />
            <input aria-label="标题" autoFocus value={newTitle} onChange={(event) => setNewTitle(event.target.value)} placeholder="会话标题" />
            <textarea aria-label="目标" value={newGoal} onChange={(event) => setNewGoal(event.target.value)} placeholder="目标（可选）" rows={2} />
            <button className="primary-btn" type="submit" disabled={busy || !newTitle.trim()}>{busy ? '创建中...' : '创建会话'}</button>
          </form>
        )}
        <div className="session-list">
          {Object.entries(sessionsByProject).map(([projectName, projectSessions]) => (
            <ProjectFolder key={projectName} projectName={projectName} sessions={projectSessions} selectedSessionId={selectedSession?.id} onSelectSession={(session) => { void handleSelectSession(session); }} />
          ))}
          {sessions.length === 0 && <div className="empty-panel">暂无会话</div>}
        </div>
      </aside>

      <main className="conversation">
        <header className="conversation-header">
          <div className="conversation-heading">
            {selectedSession && editingTitle ? (
              <div className="title-editor">
                <input aria-label="会话标题" value={titleDraft} onChange={(event) => setTitleDraft(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') void handleRename(); }} />
                <button className="subtle-btn" type="button" onClick={() => void handleRename()} disabled={busy}>保存</button>
                <button className="subtle-btn" type="button" onClick={() => setEditingTitle(false)} disabled={busy}>取消</button>
              </div>
            ) : (
              <button className="title-button" type="button" onClick={() => { if (selectedSession) { setTitleDraft(selectedSession.title); setEditingTitle(true); } }} disabled={!selectedSession}>
                {selectedSession?.title || '选择一个会话'}
              </button>
            )}
            {selectedSession && <span className={`status-chip ${selectedSession.status}`}>{selectedSession.status}</span>}
          </div>
          <div className="header-actions">
            {selectedSession && <select aria-label="会话状态" value={statusDraft} onChange={(event) => { const next = event.target.value as Session['status']; setStatusDraft(next); void handleStatus(next); }} disabled={busy || selectedSession.status === 'queued'}>
              <option value="queued" disabled>排队中</option><option value="running">运行中</option><option value="paused">已暂停</option><option value="done">已完成</option>
            </select>}
            {selectedSession?.status === 'paused' && selectedSession.run?.checkpointHash && <button className="subtle-btn header-continue-btn" type="button" onClick={() => void handleContinueSession()} disabled={busy || !continuationHealthKnown || continuationHealth?.attached !== true}>{busy ? '继续中...' : !continuationHealthKnown ? '检查编排器...' : continuationHealth?.attached !== true ? '编排器恢复中...' : '继续任务'}</button>}
            {selectedSession?.status === 'paused' && selectedSession.run?.checkpointHash && !continuationHealthKnown && <span className="continuation-health recovering" role="status">等待编排器健康状态</span>}
            <button className="icon-btn" type="button" title="切换主题" aria-label="切换主题" onClick={() => setIsDarkTheme((value) => !value)}>{isDarkTheme ? '☀' : '◐'}</button>
            <button className="icon-btn" type="button" title="打开会话详情" aria-label="打开会话详情" onClick={() => setShowDetails((value) => !value)}>▣</button>
            {selectedSession && <button className="icon-btn danger" type="button" title="删除会话" aria-label="删除会话" onClick={() => void handleDelete()} disabled={busy}>⌫</button>}
          </div>
        </header>
        {error && <div className="global-error" role="alert">{error}{pendingRetry && selectedSession?.id === pendingRetry.sessionId && <button className="subtle-btn retry-message-btn" type="button" onClick={() => void handleRetryMessage()} disabled={busy}>{busy ? '重试中...' : '重试发送'}</button>}</div>}
        {selectedSession?.run?.status === 'failed' && <div className="global-error" role="alert">{selectedSession.run.error || '任务执行失败'}。可点击“继续任务”重试。</div>}
        {(selectedSession?.run?.status === 'queued' || selectedSession?.run?.status === 'running') && <div className="stream-status" role="status">{selectedSession.run.status === 'queued' ? '任务排队中...' : '正在处理，请稍候...'}</div>}
        <div className="message-list" ref={messageListRef}>
          {selectedSession ? <MessageList key={selectedSession.id} sessionId={selectedSession.id} refreshKey={refreshKey} activeRun={selectedSession.run} /> : <div className="empty-state"><strong>选择一个会话</strong><span>从左侧打开已有会话，或新建一个。</span></div>}
        </div>
        <form className="input-area" onSubmit={(event) => void handleSendMessage(event)}>
          {selectedSession?.lastUserInput && !message && <button className="restore-input-btn" type="button" onClick={() => setMessage(selectedSession.lastUserInput || '')} disabled={busy}>恢复上次输入</button>}
          <textarea className="input-box" placeholder="输入消息，Enter 发送，Shift+Enter 换行" disabled={!selectedSession || busy || selectedSession.run?.status === 'queued' || selectedSession.run?.status === 'running'} value={message} onChange={(event) => setMessage(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); } }} />
          <button className="send-btn" type="submit" disabled={!selectedSession || busy || selectedSession.run?.status === 'queued' || selectedSession.run?.status === 'running' || !message.trim()}>{busy ? '处理中...' : '发送'}</button>
        </form>
      </main>

      <aside className={`task-panel ${showDetails ? 'open' : ''}`}>
        <div className="task-panel-header">
          <span>会话状态</span>
          <button className="icon-btn" type="button" title="关闭会话详情" aria-label="关闭会话详情" onClick={() => setShowDetails(false)}>×</button>
        </div>
        <div className="task-list">
          {selectedSession ? <SessionStatus session={selectedSession} onContinue={() => void handleContinueSession()} busy={busy} continuationHealth={continuationHealth} continuationHealthKnown={continuationHealthKnown} /> : <div className="empty-panel">未选择</div>}
        </div>
        {selectedSession && <CheckpointPanel session={selectedSession} refreshKey={refreshKey} onChanged={refreshSelected} continuationHealth={continuationHealth} continuationHealthKnown={continuationHealthKnown} onRunChanged={(run) => {
          setSelectedSession((current) => current && current.id === run.sessionId ? { ...current, run } : current);
          setSessions((items) => items.map((item) => item.id === run.sessionId ? { ...item, run } : item));
        }} />}
      </aside>
    </div>
  );
}

interface ProjectFolderProps { projectName: string; sessions: Session[]; selectedSessionId?: string; onSelectSession: (session: Session) => void }

function ProjectFolder({ projectName, sessions, selectedSessionId, onSelectSession }: ProjectFolderProps) {
  const [collapsed, setCollapsed] = useState(false);
  return <div className={`project-folder ${collapsed ? 'collapsed' : ''}`}>
    <button className="project-header" type="button" onClick={() => setCollapsed((value) => !value)}>
      <span className="project-chevron">▼</span><span className="project-icon">□</span><span className="project-name">{projectName}</span><span className="project-count">{sessions.length}</span>
    </button>
    <div className="thread-list">{sessions.map((session) => <button key={session.id} type="button" className={`thread-item ${session.id === selectedSessionId ? 'active' : ''}`} onClick={() => onSelectSession(session)}>
      <div className="thread-title-row"><span className={`status-dot ${session.status}`} /><span className="thread-title">{session.title}</span></div>
      <div className="thread-meta"><span>{session.eventCount} events</span><span>{new Date(session.updatedAt).toLocaleDateString()}</span></div>
    </button>)}</div>
  </div>;
}

function SessionStatus({ session, onContinue, busy, continuationHealth, continuationHealthKnown }: { session: Session; onContinue: () => void; busy: boolean; continuationHealth: ContinuationRuntimeStatus | null; continuationHealthKnown: boolean }) {
  const runLabel = session.run?.status === 'queued' ? '排队中'
    : session.run?.status === 'running' ? '运行中'
      : session.run?.status === 'completed' ? '已完成'
        : session.run?.status === 'failed' ? '失败' : '';
  return <div className="session-status">
    {session.planTodo && <details className="plan-todo-panel" open={session.status === 'running' || session.status === 'queued'}>
      <summary>任务进度 <span className="plan-todo-revision">v{session.planTodo.revision}</span></summary>
      {session.planTodo.plan.steps && session.planTodo.plan.steps.length > 0 && <div className="plan-summary">阶段 {Math.min(session.planTodo.plan.currentIndex + 1, session.planTodo.plan.steps.length)}/{session.planTodo.plan.steps.length}：{session.planTodo.plan.steps[session.planTodo.plan.currentIndex] || '已完成'}</div>}
      <div className="todo-items">{session.planTodo.todos.map((item, index) => <div className="todo-item" key={`${item.content}-${index}`}><span className={`todo-state ${item.status}`}>{item.status === 'completed' ? '✓' : item.status === 'in_progress' ? '•' : '○'}</span><span>{item.activeForm || item.content}</span></div>)}</div>
    </details>}
    <div className="status-row"><span>项目</span><strong>{session.projectName}</strong></div>
    <div className="status-row"><span>事件</span><strong>{session.eventCount}</strong></div>
    <div className="status-row"><span>更新</span><strong>{new Date(session.updatedAt).toLocaleString()}</strong></div>
    {runLabel && <div className="status-row"><span>断点运行</span><strong className={`run-status ${session.run?.status}`}>{runLabel}</strong></div>}
    <div className="status-row"><span>执行 worker</span><strong>{session.run?.workerId || '待分配'}</strong></div>
    {session.run && <div className="lineage-block" aria-label="运行 lineage"><div className="status-row"><span>运行 lineage</span><strong className="mono-value">{session.run.runId.slice(0, 12)}…</strong></div><div className="status-row"><span>重试次数</span><strong>第 {session.run.attempt} 次</strong></div><div className="status-row"><span>请求标识</span><strong className="mono-value">{session.run.requestId.slice(0, 12)}…</strong></div>{session.run.retryOfRunId && <div className="status-row"><span>承接运行</span><strong className="mono-value">{session.run.retryOfRunId.slice(0, 12)}…</strong></div>}{session.run.retryOfRunIds && session.run.retryOfRunIds.length > 1 && <div className="status-row"><span>历史失败</span><strong>{session.run.retryOfRunIds.length} 个</strong></div>}</div>}
    {continuationHealthKnown && continuationHealth && (continuationHealth.attached === false || continuationHealth.last_health_error) && <div className="continuation-runtime-card" role="status"><div className="status-row"><span>编排器</span><strong className={`run-status ${continuationHealth.attached ? 'running' : 'queued'}`}>{continuationHealth.attached ? '已连接（有恢复告警）' : '恢复中'}</strong></div>{continuationHealth.consecutive_failures > 0 && <div className="status-row"><span>连续失败</span><strong>{continuationHealth.consecutive_failures} 次</strong></div>}{continuationHealth.next_retry_at && <div className="status-row"><span>下次重试</span><strong>{new Date(continuationHealth.next_retry_at).toLocaleTimeString()}</strong></div>}{continuationHealth.last_health_error && <div className="recovery-summary-meta">{continuationHealth.last_health_error}</div>}</div>}
    {session.status === 'paused' && session.run?.checkpointHash && <button className="primary-btn continue-session-btn" type="button" onClick={onContinue} disabled={busy || !continuationHealthKnown || continuationHealth?.attached !== true}>{busy ? '继续中...' : !continuationHealthKnown ? '检查编排器...' : continuationHealth?.attached !== true ? '编排器恢复中...' : '继续历史任务'}</button>}
    <div className="status-row"><span>记忆事件</span><strong>{session.eventCount} 条（ledger）</strong></div>
    {session.run?.checkpointHash && <div className="status-row"><span>恢复锚点</span><strong className="mono-value">{session.run.checkpointHash.slice(0, 12)}…</strong></div>}
    {session.run?.error && <div className="inline-error" role="status">{session.run.error}</div>}
    {session.goal && <div className="goal-block"><span>目标</span><p>{session.goal}</p></div>}
  </div>;
}

function MessageList({ sessionId, refreshKey, activeRun }: { sessionId: string; refreshKey: number; activeRun?: Session['run'] }) {
  const [events, setEvents] = useState<SessionEvent[]>([]);
  const [eventFilter, setEventFilter] = useState<'all' | 'messages' | 'tools' | 'approvals' | 'code' | 'progress'>('messages');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
	const [decidingApproval, setDecidingApproval] = useState('');
  const cursorRef = useRef(-1);
  const loadedSessionRef = useRef('');
  const loadEvents = useCallback(async (after = -1) => {
    try {
      // The ledger is the durable conversation history; an arbitrary client
      // cap would make long sessions appear to forget their earliest turns.
      // Once a session is loaded, refreshes only request the suffix after the
      // last observed ledger sequence.
      const data = await api.listEvents(sessionId, after >= 0 ? { after } : {});
    setEvents((previous) => {
        const merged = mergeSessionEvents(previous, data);
        cursorRef.current = merged.reduce((max, event) => Math.max(max, event.seq), -1);
        persistedSessionCursor(sessionId, cursorRef.current);
        return merged;
      });
      setError('');
    } catch (cause) {
      setError(errorMessage(cause, '事件加载失败'));
    } finally { setLoading(false); }
  }, [sessionId]);
  useEffect(() => {
    const sameSession = loadedSessionRef.current === sessionId;
    if (!sameSession) {
      loadedSessionRef.current = sessionId;
      cursorRef.current = -1;
      setEventFilter('messages');
      setEvents([]);
      setLoading(true);
    }
    void loadEvents(sameSession ? cursorRef.current : -1);
  }, [loadEvents, refreshKey, sessionId]);
  const cursor = events.reduce((max, event) => Math.max(max, event.seq), -1);
  const activeEvents = deriveActiveEvents(events);
  const executionSummary = activeEvents.reduce((summary, event) => {
    const kind = eventKind(event);
    if (kind === 'tools') summary.tools += 1;
    else if (kind === 'approvals') summary.approvals += 1;
    else if (kind === 'code') summary.codeChanges += 1;
    else if (kind === 'progress') summary.progress += 1;
    else if (event.type === 'user/message' || event.type === 'assistant/message') summary.messages += 1;
    return summary;
  }, { tools: 0, approvals: 0, codeChanges: 0, messages: 0, progress: 0 });
  const progressEvents = activeEvents.filter((event) => event.progress || isCompactionEvent(event));
  const latestProgress = progressEvents[progressEvents.length - 1];
	const decidedApprovals = new Set(activeEvents
		.filter((event) => event.approval && event.approval.decision !== 'pending')
		.map((event) => `${event.approval?.runId}\0${event.approval?.toolCallId}`));
  const visibleEvents = eventFilter === 'all' ? activeEvents : activeEvents.filter((event) => eventFilter === 'messages'
    ? event.type === 'user/message' || event.type === 'assistant/message'
      || (event.approval?.decision === 'pending'
        && !decidedApprovals.has(`${event.approval.runId}\0${event.approval.toolCallId}`)
        && event.approval.runId === activeRun?.runId
        && (activeRun.status === 'queued' || activeRun.status === 'running'))
    : eventKind(event) === eventFilter);
	const handleApproval = useCallback(async (event: SessionEvent, decision: 'approved' | 'denied') => {
		if (!event.approval || event.approval.decision !== 'pending') return;
		const key = `${event.approval.runId}\0${event.approval.toolCallId}`;
		setDecidingApproval(key);
		try {
			const decided = await api.decideToolApproval(
				sessionId, event.approval.runId, event.approval.toolCallId,
				decision, event.id, event.seq,
			);
			if (decided) {
				setEvents((previous) => {
					const merged = mergeSessionEvents(previous, [decided]);
					cursorRef.current = merged.reduce((max, item) => Math.max(max, item.seq), -1);
					persistedSessionCursor(sessionId, cursorRef.current);
					return merged;
				});
			}
			setError('');
		} catch (cause) {
			setError(errorMessage(cause, '审批失败'));
		} finally {
			setDecidingApproval('');
		}
	}, [sessionId]);
  const handleLiveEvent = useCallback((event: SessionEvent) => {
    const currentCursor = cursorRef.current;
    if (hasSequenceGap(currentCursor, [event])) {
      // A reconnect can miss a suffix if the server rotated the connection
      // between ticket issuance and replay. Re-read the canonical ledger
      // instead of presenting a silently incomplete conversation.
      void loadEvents(-1);
      return;
    }
      setEvents((previous) => {
      const merged = mergeSessionEvents(previous, [event]);
      cursorRef.current = merged.reduce((max, item) => Math.max(max, item.seq), -1);
      persistedSessionCursor(sessionId, cursorRef.current);
      return merged;
    });
  }, [loadEvents]);
  const socket = useWebSocket({ sessionId, after: cursor, onMessage: handleLiveEvent });
  if (loading && events.length === 0) return <div className="empty-state">正在加载事件...</div>;
  return <div className="message-stream">
    <div className="stream-status"><span className={`connection-dot ${socket.state}`} />{socket.state === 'connected' ? '实时' : socket.state === 'reconnecting' ? '重连中' : '离线'}<span className="execution-summary" aria-label="执行记录摘要">消息 {executionSummary.messages} · 工具 {executionSummary.tools} · 审批 {executionSummary.approvals} · 修改 {executionSummary.codeChanges} · 进展 {executionSummary.progress}</span><label className="event-filter">筛选<select aria-label="执行记录筛选" value={eventFilter} onChange={(event) => setEventFilter(event.target.value as typeof eventFilter)}><option value="all">全部</option><option value="messages">消息</option><option value="tools">工具</option><option value="approvals">审批</option><option value="code">修改</option><option value="progress">进展</option></select></label>{socket.lastError && <span>{socket.lastError}</span>}</div>
     {latestProgress && (() => { const progress = latestProgress.progress || compactionPresentation(latestProgress); return progress && <div className="latest-progress" role="status" aria-label="当前进展"><span className="latest-progress-label">当前进展</span><strong>{progress.title}</strong><span>{progress.summary}</span>{progressEvents.length > 1 && <details><summary>查看历史进展（{progressEvents.length}）</summary><div className="progress-history">{progressEvents.slice(0, -1).reverse().map((event) => { const item = event.progress || compactionPresentation(event); return item && <div key={event.id}><b>{item.title}</b><span>{item.summary}</span></div>; })}</div></details>}</div>; })()}
    {error && <div className="inline-error" role="alert">{error}</div>}
	    {visibleEvents.length === 0 ? <div className="empty-state"><strong>{activeEvents.length === 0 ? '还没有消息' : '没有匹配的执行记录'}</strong><span>{activeEvents.length === 0 ? '发送第一条消息开始这个会话。' : '切换筛选条件查看其他事件。'}</span></div> : visibleEvents.map((event) => {
			const approvalKey = event.approval ? `${event.approval.runId}\0${event.approval.toolCallId}` : '';
			const approvalPending = event.approval?.decision === 'pending'
				&& !decidedApprovals.has(approvalKey)
				&& event.approval.runId === activeRun?.runId
				&& (activeRun.status === 'queued' || activeRun.status === 'running');
			return <article key={event.id} className={`message ${event.author} ${event.approval ? 'approval-event' : ''}`}>
      <div className="message-author"><span>{event.author === 'user' ? '你' : event.author}<span className={`event-kind ${eventKind(event)}`}>{eventKindLabel(eventKind(event))}</span></span><time>#{event.seq}</time></div>
      <div className="message-content">{(event.progress || isCompactionEvent(event)) ? renderProgressContent(event) : <>{renderToolContent(event)}{event.toolOutput ? <details className="tool-details"><summary>查看工具输出</summary><pre>{event.toolOutput}</pre></details> : null}{event.approval?.argumentsJson && <pre className="approval-arguments">{event.approval.argumentsJson}</pre>}{event.codeModification && <div className="code-receipt"><span>{event.codeModification.path}</span><code>{event.codeModification.diffSha256.slice(0, 12)}</code></div>}</>}</div>
			{approvalPending && <div className="approval-actions" aria-label={`${event.approval?.toolName} 工具审批`}>
				<button className="approval-btn approve" type="button" onClick={() => void handleApproval(event, 'approved')} disabled={decidingApproval !== ''}>批准</button>
				<button className="approval-btn deny" type="button" onClick={() => void handleApproval(event, 'denied')} disabled={decidingApproval !== ''}>拒绝</button>
				{decidingApproval === approvalKey && <span role="status">提交中...</span>}
			</div>}
	    </article>;
		})}
  </div>;
}

function AppWithAuth() {
  const [authState, setAuthState] = useState<'checking' | 'authenticated' | 'anonymous'>('checking');
  useEffect(() => {
    void api.currentUser().then(() => setAuthState('authenticated')).catch(() => setAuthState('anonymous'));
  }, []);
  if (authState === 'checking') {
    return <div className="loading-screen">正在验证登录...</div>;
  }
  if (authState === 'anonymous') return <LoginPage onLoginSuccess={() => setAuthState('authenticated')} />;
  return <App />;
}

export default AppWithAuth;
