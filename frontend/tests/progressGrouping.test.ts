import assert from 'node:assert/strict';
import test from 'node:test';
import type { SessionEvent } from '../src/types';
import { groupProgressByMessage, hideGroupedProgress } from '../src/progressGrouping.ts';

function event(id: string, seq: number, type: string, content = id): SessionEvent {
  return { id, seq, type, author: type === 'user/message' ? 'user' : 'system', content } as SessionEvent;
}

test('groupProgressByMessage attaches each progress event to the preceding user turn', () => {
  const first = event('user-1', 1, 'user/message', '先检查');
  const progress = { ...event('progress-1', 2, 'session/progress', '读取目录'), progress: { kind: 'phase', title: '检查', summary: '读取目录', sourceEventSeq: 1 } } as SessionEvent;
  const second = event('user-2', 3, 'user/message', '继续');

  const grouped = groupProgressByMessage([first, progress, second]);

  assert.deepEqual(grouped.get('user-1')?.map((item) => item.id), ['progress-1']);
  assert.equal(grouped.has('user-2'), false);
});

test('hideGroupedProgress removes nested progress from the main timeline but keeps ordinary events', () => {
  const user = event('user-1', 1, 'user/message');
  const progress = { ...event('progress-1', 2, 'session/progress'), progress: { kind: 'phase', title: '检查', summary: '读取目录', sourceEventSeq: 1 } } as SessionEvent;
  const assistant = event('assistant-1', 3, 'assistant/message');
  const grouped = groupProgressByMessage([user, progress, assistant]);

  assert.deepEqual(hideGroupedProgress([user, progress, assistant], grouped).map((item) => item.id), ['user-1', 'assistant-1']);
});
