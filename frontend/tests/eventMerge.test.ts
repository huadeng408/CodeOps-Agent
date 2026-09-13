import assert from 'node:assert/strict';
import test from 'node:test';
import type { SessionEvent } from '../src/types';
import { mergeSessionEvents } from '../src/eventMerge.ts';

function event(id: string, seq: number, content = id): SessionEvent {
  return {
    id,
    seq,
    type: 'user/message',
    author: 'user',
    content,
  } as SessionEvent;
}

test('mergeSessionEvents replays the same event idempotently and keeps ledger order', () => {
    const merged = mergeSessionEvents(
      [event('e2', 2), event('e1', 1)],
      [event('e1', 1), event('e3', 3)],
    );

    assert.deepEqual(merged.map((item) => item.id), ['e1', 'e2', 'e3']);
});

test('mergeSessionEvents does not render two facts for one canonical ledger sequence during reconnect replay', () => {
    const merged = mergeSessionEvents(
      [event('e1', 1, 'original')],
      [event('transport-replay-e1', 1, 'replayed')],
    );

    assert.equal(merged.length, 1);
    assert.equal(merged[0]?.seq, 1);
    assert.equal(merged[0]?.content, 'replayed');
});
