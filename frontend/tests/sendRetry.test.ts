import assert from 'node:assert/strict';
import test from 'node:test';
import { retryPendingMessage, sendFailureMessage } from '../src/sendRetry.ts';

test('retry keeps request lineage while using the latest ledger cursor', async () => {
  const calls: unknown[] = [];
  const result = await retryPendingMessage(
    { sessionId: 'session-a', content: '继续任务', requestId: 'request-1' },
    async () => ({ sessionId: 'session-a', eventCount: 17 }),
    async (request) => { calls.push(request); return 'accepted'; },
  );

  assert.equal(result, 'accepted');
  assert.deepEqual(calls, [{
    sessionId: 'session-a',
    content: '继续任务',
    requestId: 'request-1',
    expectedSeq: 17,
  }]);
});

test('retry rejects a cursor from another session', async () => {
  await assert.rejects(
    () => retryPendingMessage(
      { sessionId: 'session-a', content: '继续任务', requestId: 'request-1' },
      async () => ({ sessionId: 'session-b', eventCount: 17 }),
      async () => 'accepted',
    ),
    /retry session mismatch/,
  );
});

test('send failure explains that a network outage is recoverable', () => {
  assert.equal(
    sendFailureMessage(new TypeError('Failed to fetch')),
    '无法连接后端，消息已保留；服务恢复后可重试发送',
  );
});

test('send failure gives actionable messages for conflict and server errors', () => {
  assert.equal(
    sendFailureMessage({ status: 409, message: 'conflict' }),
    '会话已被其他操作更新，消息已保留；请重试发送',
  );
  assert.equal(
    sendFailureMessage({ status: 502, message: 'Bad Gateway' }),
    '后端暂时不可用（HTTP 502），消息已保留；服务恢复后可重试发送',
  );
});
