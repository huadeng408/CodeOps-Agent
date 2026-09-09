import { useEffect, useRef, useState } from 'react';
import type { SessionEvent } from './types';
import { api } from './api';

interface UseWebSocketOptions {
  sessionId: string | null;
  after?: number;
  onMessage?: (event: SessionEvent) => void;
}

function persistedCursor(sessionId: string, fallback: number): number {
  if (fallback >= 0) return fallback;
  try {
    const raw = localStorage.getItem("codeops:ledger-cursor:" + sessionId);
    if (raw === null) return fallback;
    const cursor = Number.parseInt(raw, 10);
    return Number.isSafeInteger(cursor) && cursor >= -1 ? cursor : fallback;
  } catch {
    return fallback;
  }
}

export type WebSocketState = 'idle' | 'connecting' | 'connected' | 'reconnecting' | 'error';

// The browser never receives an access JWT. Each connection obtains a
// short-lived, one-use session ticket over the authenticated REST channel.
export function useWebSocket({ sessionId, after = -1, onMessage }: UseWebSocketOptions) {
  const [state, setState] = useState<WebSocketState>('idle');
  const [lastError, setLastError] = useState('');
  const afterRef = useRef(after);
  const callbackRef = useRef(onMessage);

  useEffect(() => { afterRef.current = after; }, [after]);
  useEffect(() => { callbackRef.current = onMessage; }, [onMessage]);

  useEffect(() => {
    if (!sessionId) {
      setState('idle');
      return undefined;
    }

    let stopped = false;
    let socket: WebSocket | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
    let attempt = 0;
    let generation = 0;

    const connect = async () => {
      if (stopped) return;
      const connectionGeneration = ++generation;
      setState(attempt === 0 ? 'connecting' : 'reconnecting');
      try {
        const { ticket } = await api.issueWebSocketTicket(sessionId);
        if (stopped || connectionGeneration !== generation) return;
        const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws';
        const resumeAfter = persistedCursor(sessionId, afterRef.current);
        const url = `${scheme}://${window.location.host}/api/v1/sessions/${encodeURIComponent(sessionId)}/ws?ticket=${encodeURIComponent(ticket)}&after=${resumeAfter}`;
        const currentSocket = new WebSocket(url);
        socket = currentSocket;

        currentSocket.onopen = () => {
          if (stopped || connectionGeneration !== generation) return;
          attempt = 0;
          setLastError('');
          setState('connected');
        };
        currentSocket.onmessage = (event) => {
          if (stopped || connectionGeneration !== generation) return;
          try {
            const parsed = JSON.parse(event.data) as SessionEvent;
            if (parsed && parsed.id) callbackRef.current?.(parsed);
          } catch {
            setLastError('收到无法识别的实时事件');
          }
        };
        currentSocket.onerror = () => {
          if (stopped || connectionGeneration !== generation) return;
          setLastError('实时连接暂时不可用');
          setState('error');
        };
        currentSocket.onclose = () => {
          if (connectionGeneration !== generation) return;
          if (socket === currentSocket) socket = null;
          if (stopped) return;
          attempt += 1;
          const delay = Math.min(1000 * 2 ** Math.min(attempt - 1, 3), 8000);
          setState('reconnecting');
          reconnectTimer = setTimeout(connect, delay);
        };
      } catch (error) {
        if (stopped || connectionGeneration !== generation) return;
        attempt += 1;
        setLastError(error instanceof Error ? error.message : '实时连接失败');
        setState('reconnecting');
        reconnectTimer = setTimeout(connect, Math.min(1000 * 2 ** Math.min(attempt - 1, 3), 8000));
      }
    };

    void connect();
    return () => {
      stopped = true;
      generation += 1;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      socket?.close();
      socket = null;
    };
  }, [sessionId]);

  return { connected: state === 'connected', state, lastError };
}
