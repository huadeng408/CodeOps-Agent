import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Session, SessionEvent } from './types';
import { ApiError, api } from './api';
import { useWebSocket } from './useWebSocket';
import { CheckpointPanel } from './CheckpointPanel';
import { LoginPage } from './LoginPage';

function mergeEvents(existing: SessionEvent[], incoming: SessionEvent[]): SessionEvent[] {
  const byID = new Map<string, SessionEvent>();
  for (const event of [...existing, ...incoming]) byID.set(event.id, event);
  return [...byID.values()].sort((a, b) => a.seq - b.seq);
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

type EventKind = 'messages' | 'tools' | 'approvals' | 'code';

function eventKind(event: SessionEvent): EventKind {
  if (event.toolName || event.type.startsWith('tool/')) return 'tools';
  if (event.type.includes('approval')) return 'approvals';
  if (event.type.includes('patch') || event.type.includes('code')) return 'code';
  return 'messages';
}

function eventKindLabel(kind: EventKind): string {
  return kind === 'tools' ? '工具' : kind === 'approvals' ? '审批' : kind === 'code' ? '修改' : '消息';
}

function App() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [selectedSession, setSelectedSession] = useState<Session | null>(null);
  const [message, setMessage] = useState('');
  const [isDarkTheme, setIsDarkTheme] = useState(() => localStorage.getItem('theme') === 'dark');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [refreshKey, setRefreshKey] = useState(0);
  const [showCreate, setShowCreate] = useState(false);
  const [newTitle, setNewTitle] = useState('');
  const [newProject, setNewProject] = useState('default-project');
  const [newGoal, setNewGoal] = useState('');
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState('');
  const [statusDraft, setStatusDraft] = useState<Session['status']>('running');
  const [showDetails, setShowDetails] = useState(false);
  const selectionGenerationRef = useRef(0);

  useEffect(() => {
    document.body.toggleAttribute('data-ds-dark-theme', isDarkTheme);
    localStorage.setItem('theme', isDarkTheme ? 'dark' : 'light');
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
      setMessage(localStorage.getItem(sessionDraftKey(selectedSession.id)) || '');
    } catch {
      setMessage('');
    }
  }, [selectedSession?.id]);

  useEffect(() => {
    if (!selectedSession) return;
    try {
      const draft = message.trim();
      if (draft) localStorage.setItem(sessionDraftKey(selectedSession.id), message);
      else localStorage.removeItem(sessionDraftKey(selectedSession.id));
    } catch {
      // Draft persistence is a convenience; canonical ledger recovery does
      // not depend on browser storage being available.
    }
  }, [message, selectedSession?.id]);

  const syncSessions = useCallback(async (preferredID?: string) => {
    const data = await api.listSessions();
    setSessions(data);
    setSelectedSession((current) => {
      const remembered = localStorage.getItem('codeops:selected-session');
      const wanted = preferredID || current?.id || remembered;
      const next = (wanted && data.find((session) => session.id === wanted)) || data[0] || null;
      if (next) localStorage.setItem('codeops:selected-session', next.id);
      else localStorage.removeItem('codeops:selected-session');
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
    const generation = ++selectionGenerationRef.current;
    setSelectedSession(session);
    localStorage.setItem('codeops:selected-session', session.id);
    setStatusDraft(session.status);
    setEditingTitle(false);
    setError('');
    // Sidebar entries are a convenient index, not the recovery source. Read
    // the canonical ledger projection whenever a task is reopened so run,
    // checkpoint, retry lineage and status survive browser reloads.
    try {
      const fresh = await api.getSession(session.id);
      if (generation !== selectionGenerationRef.current) return;
      setSelectedSession(fresh);
      setSessions((items) => items.map((item) => item.id === fresh.id ? fresh : item));
      setStatusDraft(fresh.status);
      setRefreshKey((value) => value + 1);
    } catch (cause) {
      setError(errorMessage(cause, '会话恢复状态加载失败'));
    }
  }, []);

  useEffect(() => {
    void syncSessions().catch((cause) => setError(errorMessage(cause, '会话加载失败'))).finally(() => setLoading(false));
  }, [syncSessions]);

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
    setBusy(true);
    setError('');
    try {
      await api.deleteSession(selectedSession.id, selectedSession.eventCount);
      await syncSessions();
    } catch (cause) {
      setError(errorMessage(cause, '删除会话失败'));
      await refreshSelected().catch(() => undefined);
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
    try {
      await api.createEvent(selectedSession.id, content, selectedSession.eventCount);
      setMessage('');
      try { localStorage.removeItem(sessionDraftKey(selectedSession.id)); } catch { /* best effort */ }
      await refreshSelected();
    } catch (cause) {
      setError(errorMessage(cause, '消息发送失败'));
      await refreshSelected().catch(() => undefined);
    } finally {
      setBusy(false);
    }
  };

  const handleContinueSession = async () => {
    if (!selectedSession || busy || selectedSession.status !== 'paused' || !selectedSession.run?.checkpointHash) return;
    setBusy(true);
    setError('');
    const checkpointHash = selectedSession.run.checkpointHash;
    const storageKey = `continuation-request:${selectedSession.id}:${checkpointHash}`;
    const requestId = localStorage.getItem(storageKey) || `browser:${crypto.randomUUID()}`;
    localStorage.setItem(storageKey, requestId);
    try {
      await api.continueSession(selectedSession.id, selectedSession.eventCount, checkpointHash, requestId);
      await refreshSelected();
    } catch (cause) {
      setError(errorMessage(cause, '继续历史任务失败'));
      await refreshSelected().catch(() => undefined);
    } finally {
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
            {selectedSession?.status === 'paused' && selectedSession.run?.checkpointHash && <button className="subtle-btn header-continue-btn" type="button" onClick={() => void handleContinueSession()} disabled={busy}>{busy ? '继续中...' : '继续任务'}</button>}
            <button className="icon-btn" type="button" title="切换主题" aria-label="切换主题" onClick={() => setIsDarkTheme((value) => !value)}>{isDarkTheme ? '☀' : '◐'}</button>
            <button className="icon-btn" type="button" title="打开会话详情" aria-label="打开会话详情" onClick={() => setShowDetails((value) => !value)}>▣</button>
            {selectedSession && <button className="icon-btn danger" type="button" title="删除会话" aria-label="删除会话" onClick={() => void handleDelete()} disabled={busy}>⌫</button>}
          </div>
        </header>
        {error && <div className="global-error" role="alert">{error}</div>}
        <div className="message-list">
          {selectedSession ? <MessageList key={selectedSession.id} sessionId={selectedSession.id} refreshKey={refreshKey} /> : <div className="empty-state"><strong>选择一个会话</strong><span>从左侧打开已有会话，或新建一个。</span></div>}
        </div>
        <form className="input-area" onSubmit={(event) => void handleSendMessage(event)}>
          {selectedSession?.lastUserInput && !message && <button className="restore-input-btn" type="button" onClick={() => setMessage(selectedSession.lastUserInput || '')} disabled={busy}>恢复上次输入</button>}
          <textarea className="input-box" placeholder="输入消息，Enter 发送，Shift+Enter 换行" disabled={!selectedSession || busy} value={message} onChange={(event) => setMessage(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); } }} />
          <button className="send-btn" type="submit" disabled={!selectedSession || busy || !message.trim()}>{busy ? '处理中...' : '发送'}</button>
        </form>
      </main>

      <aside className={`task-panel ${showDetails ? 'open' : ''}`}>
        <div className="task-panel-header">
          <span>会话状态</span>
          <button className="icon-btn" type="button" title="关闭会话详情" aria-label="关闭会话详情" onClick={() => setShowDetails(false)}>×</button>
        </div>
        <div className="task-list">
          {selectedSession ? <SessionStatus session={selectedSession} onContinue={() => void handleContinueSession()} busy={busy} /> : <div className="empty-panel">未选择</div>}
        </div>
        {selectedSession && <CheckpointPanel session={selectedSession} refreshKey={refreshKey} onChanged={refreshSelected} onRunChanged={(run) => {
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

function SessionStatus({ session, onContinue, busy }: { session: Session; onContinue: () => void; busy: boolean }) {
  const runLabel = session.run?.status === 'queued' ? '排队中'
    : session.run?.status === 'running' ? '运行中'
      : session.run?.status === 'completed' ? '已完成'
        : session.run?.status === 'failed' ? '失败' : '';
  return <div className="session-status">
    <div className="status-row"><span>项目</span><strong>{session.projectName}</strong></div>
    <div className="status-row"><span>事件</span><strong>{session.eventCount}</strong></div>
    <div className="status-row"><span>更新</span><strong>{new Date(session.updatedAt).toLocaleString()}</strong></div>
    {runLabel && <div className="status-row"><span>断点运行</span><strong className={`run-status ${session.run?.status}`}>{runLabel}</strong></div>}
    <div className="status-row"><span>执行 worker</span><strong>{session.run?.workerId || '待分配'}</strong></div>
    {session.run && <div className="lineage-block" aria-label="运行 lineage"><div className="status-row"><span>运行 lineage</span><strong className="mono-value">{session.run.runId.slice(0, 12)}…</strong></div><div className="status-row"><span>重试次数</span><strong>第 {session.run.attempt} 次</strong></div><div className="status-row"><span>请求标识</span><strong className="mono-value">{session.run.requestId.slice(0, 12)}…</strong></div>{session.run.retryOfRunId && <div className="status-row"><span>承接运行</span><strong className="mono-value">{session.run.retryOfRunId.slice(0, 12)}…</strong></div>}{session.run.retryOfRunIds && session.run.retryOfRunIds.length > 1 && <div className="status-row"><span>历史失败</span><strong>{session.run.retryOfRunIds.length} 个</strong></div>}</div>}
    {session.status === 'paused' && session.run?.checkpointHash && <button className="primary-btn continue-session-btn" type="button" onClick={onContinue} disabled={busy}>{busy ? '继续中...' : '继续历史任务'}</button>}
    <div className="status-row"><span>记忆事件</span><strong>{session.eventCount} 条（ledger）</strong></div>
    {session.run?.checkpointHash && <div className="status-row"><span>恢复锚点</span><strong className="mono-value">{session.run.checkpointHash.slice(0, 12)}…</strong></div>}
    {session.run?.error && <div className="inline-error" role="status">{session.run.error}</div>}
    {session.goal && <div className="goal-block"><span>目标</span><p>{session.goal}</p></div>}
  </div>;
}

function MessageList({ sessionId, refreshKey }: { sessionId: string; refreshKey: number }) {
  const [events, setEvents] = useState<SessionEvent[]>([]);
  const [eventFilter, setEventFilter] = useState<'all' | 'messages' | 'tools' | 'approvals' | 'code'>('all');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
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
        const merged = mergeEvents(previous, data);
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
      setEventFilter('all');
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
    else summary.messages += 1;
    return summary;
  }, { tools: 0, approvals: 0, codeChanges: 0, messages: 0 });
  const visibleEvents = eventFilter === 'all' ? activeEvents : activeEvents.filter((event) => eventKind(event) === eventFilter);
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
      const merged = mergeEvents(previous, [event]);
      cursorRef.current = merged.reduce((max, item) => Math.max(max, item.seq), -1);
      persistedSessionCursor(sessionId, cursorRef.current);
      return merged;
    });
  }, [loadEvents]);
  const socket = useWebSocket({ sessionId, after: cursor, onMessage: handleLiveEvent });
  if (loading && events.length === 0) return <div className="empty-state">正在加载事件...</div>;
  return <div className="message-stream">
    <div className="stream-status"><span className={`connection-dot ${socket.state}`} />{socket.state === 'connected' ? '实时' : socket.state === 'reconnecting' ? '重连中' : '离线'}<span className="execution-summary" aria-label="执行记录摘要">消息 {executionSummary.messages} · 工具 {executionSummary.tools} · 审批 {executionSummary.approvals} · 修改 {executionSummary.codeChanges}</span><label className="event-filter">筛选<select aria-label="执行记录筛选" value={eventFilter} onChange={(event) => setEventFilter(event.target.value as typeof eventFilter)}><option value="all">全部</option><option value="messages">消息</option><option value="tools">工具</option><option value="approvals">审批</option><option value="code">修改</option></select></label>{socket.lastError && <span>{socket.lastError}</span>}</div>
    {error && <div className="inline-error" role="alert">{error}</div>}
    {visibleEvents.length === 0 ? <div className="empty-state"><strong>{activeEvents.length === 0 ? '还没有消息' : '没有匹配的执行记录'}</strong><span>{activeEvents.length === 0 ? '发送第一条消息开始这个会话。' : '切换筛选条件查看其他事件。'}</span></div> : visibleEvents.map((event) => <article key={event.id} className={`message ${event.author}`}>
      <div className="message-author"><span>{event.author === 'user' ? '你' : event.author}<span className={`event-kind ${eventKind(event)}`}>{eventKindLabel(eventKind(event))}</span></span><time>#{event.seq}</time></div>
      <div className="message-content">{event.content || event.type}{event.toolOutput && <pre>{event.toolOutput}</pre>}</div>
    </article>)}
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
