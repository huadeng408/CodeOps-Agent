import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react';
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

  useEffect(() => {
    document.body.toggleAttribute('data-ds-dark-theme', isDarkTheme);
    localStorage.setItem('theme', isDarkTheme ? 'dark' : 'light');
  }, [isDarkTheme]);

  useEffect(() => {
    if (selectedSession) setStatusDraft(selectedSession.status);
  }, [selectedSession?.id, selectedSession?.status]);

  const syncSessions = useCallback(async (preferredID?: string) => {
    const data = await api.listSessions();
    setSessions(data);
    setSelectedSession((current) => {
      const wanted = preferredID || current?.id;
      return (wanted && data.find((session) => session.id === wanted)) || data[0] || null;
    });
    return data;
  }, []);

  const refreshSelected = useCallback(async () => {
    if (!selectedSession) {
      await syncSessions();
      return;
    }
    const fresh = await api.getSession(selectedSession.id);
    setSelectedSession(fresh);
    setSessions((items) => items.map((item) => item.id === fresh.id ? fresh : item));
    setRefreshKey((value) => value + 1);
  }, [selectedSession, syncSessions]);

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
      await refreshSelected();
    } catch (cause) {
      setError(errorMessage(cause, '消息发送失败'));
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
            <ProjectFolder key={projectName} projectName={projectName} sessions={projectSessions} selectedSessionId={selectedSession?.id} onSelectSession={(session) => {
              setSelectedSession(session);
              setStatusDraft(session.status);
              setEditingTitle(false);
              setError('');
            }} />
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
          {selectedSession ? <SessionStatus session={selectedSession} /> : <div className="empty-panel">未选择</div>}
        </div>
        {selectedSession && <CheckpointPanel session={selectedSession} refreshKey={refreshKey} onChanged={refreshSelected} />}
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

function SessionStatus({ session }: { session: Session }) {
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
    <div className="status-row"><span>记忆事件</span><strong>{session.eventCount} 条（ledger）</strong></div>
    {session.run?.checkpointHash && <div className="status-row"><span>恢复锚点</span><strong className="mono-value">{session.run.checkpointHash.slice(0, 12)}…</strong></div>}
    {session.run?.error && <div className="inline-error" role="status">{session.run.error}</div>}
    {session.goal && <div className="goal-block"><span>目标</span><p>{session.goal}</p></div>}
  </div>;
}

function MessageList({ sessionId, refreshKey }: { sessionId: string; refreshKey: number }) {
  const [events, setEvents] = useState<SessionEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const loadEvents = useCallback(async () => {
    try {
      // The ledger is the durable conversation history; an arbitrary client
      // cap would make long sessions appear to forget their earliest turns.
      const data = await api.listEvents(sessionId);
      setEvents((previous) => mergeEvents(previous, data));
      setError('');
    } catch (cause) {
      setError(errorMessage(cause, '事件加载失败'));
    } finally { setLoading(false); }
  }, [sessionId]);
  useEffect(() => { setEvents([]); setLoading(true); void loadEvents(); }, [loadEvents, refreshKey]);
  const cursor = events.reduce((max, event) => Math.max(max, event.seq), -1);
  const activeEvents = deriveActiveEvents(events);
  const executionSummary = activeEvents.reduce((summary, event) => {
    if (event.toolName || event.type.startsWith('tool/')) summary.tools += 1;
    else if (event.type.includes('approval')) summary.approvals += 1;
    else if (event.type.includes('patch') || event.type.includes('code')) summary.codeChanges += 1;
    else summary.messages += 1;
    return summary;
  }, { tools: 0, approvals: 0, codeChanges: 0, messages: 0 });
  const socket = useWebSocket({ sessionId, after: cursor, onMessage: (event) => setEvents((previous) => mergeEvents(previous, [event])) });
  if (loading && events.length === 0) return <div className="empty-state">正在加载事件...</div>;
  return <div className="message-stream">
    <div className="stream-status"><span className={`connection-dot ${socket.state}`} />{socket.state === 'connected' ? '实时' : socket.state === 'reconnecting' ? '重连中' : '离线'}<span className="execution-summary" aria-label="执行记录摘要">消息 {executionSummary.messages} · 工具 {executionSummary.tools} · 审批 {executionSummary.approvals} · 修改 {executionSummary.codeChanges}</span>{socket.lastError && <span>{socket.lastError}</span>}</div>
    {error && <div className="inline-error" role="alert">{error}</div>}
    {activeEvents.length === 0 ? <div className="empty-state"><strong>还没有消息</strong><span>发送第一条消息开始这个会话。</span></div> : activeEvents.map((event) => <article key={event.id} className={`message ${event.author}`}>
      <div className="message-author"><span>{event.author === 'user' ? '你' : event.author}</span><time>#{event.seq}</time></div>
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
