export interface RetrySessionCursor {
  sessionId: string;
  eventCount: number;
}

export interface PendingMessageRequest {
  sessionId: string;
  content: string;
  requestId: string;
}

export interface RetryMessageRequest extends PendingMessageRequest {
  expectedSeq: number;
}

export function isSequenceConflict(cause: unknown): boolean {
  return cause instanceof Error && 'status' in cause && cause.status === 409
    && /^session event sequence conflict(?::|$)/.test(cause.message);
}

/** Turn send failures into concise guidance without discarding the draft. */
export function sendFailureMessage(cause: unknown): string {
  const status = typeof cause === 'object' && cause !== null && 'status' in cause
    ? Number((cause as { status?: unknown }).status)
    : 0;
  if (status === 409 && cause instanceof Error && cause.message.startsWith('verification usage requires reconciliation')) {
    return '模型调用用量尚未确认，消息已保留；请先完成用量对账，再重试发送';
  }
  if (status === 409 && cause instanceof Error && cause.message.startsWith('verification batch already has another task')) {
    return '其他代码任务正在占用当前批次，消息已保留；请等待该任务完成并结束代码任务，再重试发送';
  }
  if (isSequenceConflict(cause)) return '会话已被其他操作更新，消息已保留；请重试发送';
  if (status === 409) {
    const reason = cause instanceof Error && cause.message ? cause.message + '；' : '';
    return reason + '消息未被接受，草稿已保留；请检查任务与工作区状态，恢复后再重试发送';
  }
  if (status >= 500) return '后端暂时不可用（HTTP ' + status + '），消息已保留；服务恢复后可重试发送';
  if (cause instanceof TypeError && /fetch|network|failed/i.test(cause.message)) {
    return '无法连接后端，消息已保留；服务恢复后可重试发送';
  }
  return cause instanceof Error && cause.message
    ? cause.message
    : '消息发送失败，消息已保留；可重试发送';
}

/** Reload the canonical Session before retrying, while preserving request lineage. */
export async function retryPendingMessage<T>(
  pending: PendingMessageRequest,
  loadSession: (sessionId: string) => Promise<RetrySessionCursor>,
  submit: (request: RetryMessageRequest) => Promise<T>,
): Promise<T> {
  const fresh = await loadSession(pending.sessionId);
  if (pending.sessionId !== fresh.sessionId) {
    throw new Error('retry session mismatch');
  }
  return submit({
    sessionId: pending.sessionId,
    content: pending.content,
    requestId: pending.requestId,
    expectedSeq: fresh.eventCount,
  });
}
