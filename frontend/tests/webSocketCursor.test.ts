import test from 'node:test';
import assert from 'node:assert/strict';
import { persistedCursor } from '../src/ledgerCursor.ts';

test('refresh and reconnect resume from the newest persisted ledger cursor', () => {
  const original = globalThis.localStorage;
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: () => '17',
    },
  });

  try {
    assert.equal(persistedCursor('session-a', 3), 17);
    assert.equal(persistedCursor('session-a', 21), 21);
  } finally {
    if (original === undefined) delete (globalThis as { localStorage?: Storage }).localStorage;
    else Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: original });
  }
});
