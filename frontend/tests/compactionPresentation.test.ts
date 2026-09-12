import assert from 'node:assert/strict';
import test from 'node:test';
import { compactionPresentation, isCompactionEvent, isSessionSurfaceEvent } from '../src/compactionPresentation.ts';

test('compaction events are presented as progress with a recovery-safe summary', () => {
  const event = { type: 'context/compaction', content: '保留关键约束：先验证再提交' };

  assert.equal(isCompactionEvent(event), true);
  assert.deepEqual(compactionPresentation(event), {
    kind: 'progress',
    title: '整理上下文',
    summary: '已整理较早对话，保留关键约束：先验证再提交',
  });
});

test('ordinary events do not get a compaction presentation', () => {
  assert.equal(isCompactionEvent({ type: 'assistant/message', content: '完成' }), false);
  assert.equal(compactionPresentation({ type: 'assistant/message', content: '完成' }), null);
});

test('checkpoint history keeps compaction events in the active surface', () => {
  assert.equal(isSessionSurfaceEvent({ type: 'context/compaction', content: 'summary' }), true);
  assert.equal(isSessionSurfaceEvent({ type: 'assistant/message', content: 'reply' }), true);
  assert.equal(isSessionSurfaceEvent({ type: 'session/progress', content: 'progress' }), false);
});
