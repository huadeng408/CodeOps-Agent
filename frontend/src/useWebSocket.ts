import { useEffect, useRef, useState } from 'react';
import type { SessionEvent } from './types';
import { api } from './api';

interface UseWebSocketOptions {
  sessionId: string | null;
  after?: number;
  onMessage?: (event: SessionEvent) => void;
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

    const connect = async () => {
      if (stopped) return;
      setState(attempt === 0 ? 'connecting' : 'reconnecting');
      try {
        const { ticket } = await api.issueWebSocketTicket(sessionId);
        if (stopped) return;
        const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws';
        const url = `${scheme}://${window.location.host}/api/v1/sessions/${encodeURIComponent(sessionId)}/ws?ticket=${encodeURIComponent(ticket)}&after=${afterRef.current}`;
        socket = new WebSocket(url);

        socket.onopen = () => {
          attempt = 0;
          setLastError('');
          setState('connected');
        };
        socket.onmessage = (event) => {
          try {
            const parsed = JSON.parse(event.data) as SessionEvent;
            if (parsed && parsed.id) callbackRef.current?.(parsed);
          } catch {
            setLastError('收到无法识别的实时事件');
          }
        };
        socket.onerror = () => {
          setLastError('实时连接暂时不可用');
          setState('error');
        };
        socket.onclose = () => {
          socket = null;
          if (stopped) return;
          attempt += 1;
          const delay = Math.min(1000 * 2 ** Math.min(attempt - 1, 3), 8000);
          setState('reconnecting');
          reconnectTimer = setTimeout(connect, delay);
        };
      } catch (error) {
        if (stopped) return;
        attempt += 1;
        setLastError(error instanceof Error ? error.message : '实时连接失败');
        setState('reconnecting');
        reconnectTimer = setTimeout(connect, Math.min(1000 * 2 ** Math.min(attempt - 1, 3), 8000));
      }
    };

    void connect();
    return () => {
      stopped = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      socket?.close();
      socket = null;
    };
  }, [sessionId]);

  return { connected: state === 'connected', state, lastError };
}
