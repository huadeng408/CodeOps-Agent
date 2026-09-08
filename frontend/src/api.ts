import type { Session, SessionEvent, SessionCheckpoint, SessionRun, ApiResponse } from './types';

const API_BASE = '/api/v1';

export class ApiError extends Error {
  readonly status: number;
  readonly payload: unknown;

  constructor(status: number, message: string, payload?: unknown) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.payload = payload;
  }
}

type RequestOptions = { body?: unknown; signal?: AbortSignal };

class ApiClient {
  private async request<T>(method: string, path: string, options: RequestOptions = {}): Promise<ApiResponse<T>> {
    const response = await fetch(`${API_BASE}${path}`, {
      method,
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      signal: options.signal,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    });
    const contentType = response.headers.get('content-type') || '';
    const payload = contentType.includes('json')
      ? await response.json().catch(() => undefined)
      : await response.text().catch(() => '');
    if (!response.ok) {
      const message = typeof payload === 'object' && payload !== null && 'message' in payload
        ? String((payload as { message?: unknown }).message || response.statusText)
        : response.statusText || `HTTP ${response.status}`;
      throw new ApiError(response.status, message, payload);
    }
    return payload as ApiResponse<T>;
  }

  async login(identifier: string, password: string): Promise<{ user: unknown }> {
    const normalized = identifier.trim();
    const result = await this.request<{ token?: string; refreshToken?: string; user?: unknown }>(
      'POST', '/users/login', {
        body: {
          email: '',
          username: normalized,
          password,
        },
      },
    );
    return { user: result.data?.user };
  }

  async register(identifier: string, password: string, name: string): Promise<void> {
    await this.request('POST', '/users/register', {
      body: { email: identifier, username: identifier, password, name },
    });
  }

  async currentUser(): Promise<unknown> {
    const result = await this.request<unknown>('GET', '/users/me');
    return result.data;
  }

  async logout(): Promise<void> {
    await this.request('POST', '/users/logout', { body: {} });
  }

  async refresh(): Promise<void> {
    await this.request('POST', '/auth/refreshToken', { body: {} });
  }

  async listSessions(): Promise<Session[]> {
    const result = await this.request<Session[]>('GET', '/sessions');
    return result.data || [];
  }

  async createSession(projectName: string, title: string, goal = ''): Promise<Session> {
    const result = await this.request<Session>('POST', '/sessions', { body: { projectName, title, goal } });
    return result.data;
  }

  async getSession(sessionId: string): Promise<Session> {
    const result = await this.request<Session>('GET', `/sessions/${encodeURIComponent(sessionId)}`);
    return result.data;
  }

  async updateSessionTitle(sessionId: string, title: string, expectedSeq: number): Promise<void> {
    await this.request('PUT', `/sessions/${encodeURIComponent(sessionId)}/title`, { body: { title, expectedSeq } });
  }

  async updateSessionStatus(sessionId: string, status: Session['status'], expectedSeq: number): Promise<void> {
    await this.request('PUT', `/sessions/${encodeURIComponent(sessionId)}/status`, { body: { status, expectedSeq } });
  }

  async deleteSession(sessionId: string, expectedSeq: number): Promise<void> {
    await this.request('DELETE', `/sessions/${encodeURIComponent(sessionId)}?expectedSeq=${expectedSeq}`);
  }

  async listEvents(sessionId: string, options: { limit?: number; after?: number } = {}): Promise<SessionEvent[]> {
    const params = new URLSearchParams();
    if (options.limit && options.limit > 0) params.set('limit', String(options.limit));
    if (options.after !== undefined) params.set('after', String(options.after));
    const query = params.toString() ? `?${params.toString()}` : '';
    const result = await this.request<SessionEvent[]>(
      'GET', `/sessions/${encodeURIComponent(sessionId)}/events${query}`,
    );
    return result.data || [];
  }

  async createEvent(sessionId: string, content: string, expectedSeq: number): Promise<SessionEvent> {
    const result = await this.request<SessionEvent>(
      'POST', `/sessions/${encodeURIComponent(sessionId)}/events`,
      { body: { type: 'user/message', author: 'user', content, expectedSeq } },
    );
    return result.data;
  }

  async listCheckpoints(sessionId: string): Promise<SessionCheckpoint[]> {
    const result = await this.request<SessionCheckpoint[]>(
      'GET', `/sessions/${encodeURIComponent(sessionId)}/checkpoints`,
    );
    return result.data || [];
  }

  async createCheckpoint(sessionId: string, eventId: string, label: string, expectedSeq: number): Promise<SessionCheckpoint> {
    const result = await this.request<SessionCheckpoint>(
      'POST', `/sessions/${encodeURIComponent(sessionId)}/checkpoints`,
      { body: { eventId, label, expectedSeq } },
    );
    return result.data;
  }

  async restoreCheckpoint(sessionId: string, hash: string, expectedSeq: number): Promise<SessionEvent> {
    const result = await this.request<SessionEvent>(
      'POST', `/sessions/${encodeURIComponent(sessionId)}/restore/${encodeURIComponent(hash)}`,
      { body: { expectedSeq } },
    );
    return result.data;
  }

  async continueSession(sessionId: string, expectedSeq: number, checkpointHash = '', requestId = ''): Promise<SessionRun> {
    const result = await this.request<SessionRun>(
      'POST', `/sessions/${encodeURIComponent(sessionId)}/continue`,
      { body: { checkpointHash, expectedSeq, requestId } },
    );
    return result.data;
  }

  async issueWebSocketTicket(sessionId: string): Promise<{ ticket: string; expiresAt: string }> {
    const result = await this.request<{ ticket: string; expiresAt: string }>(
      'POST', `/sessions/${encodeURIComponent(sessionId)}/ws-ticket`, { body: {} },
    );
    return result.data;
  }
}

export const api = new ApiClient();
