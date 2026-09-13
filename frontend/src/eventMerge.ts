import type { SessionEvent } from './types';

/**
 * Merge REST replay and WebSocket facts into one ordered ledger projection.
 * The canonical sequence is the idempotency key for a session; event ids are
 * retained as a second guard for malformed/replayed transport payloads.
 */
export function mergeSessionEvents(existing: SessionEvent[], incoming: SessionEvent[]): SessionEvent[] {
  const bySequence = new Map<number, SessionEvent>();
  const byId = new Map<string, SessionEvent>();
  for (const event of [...existing, ...incoming]) {
    const priorBySequence = bySequence.get(event.seq);
    const priorById = byId.get(event.id);
    if (priorBySequence && priorBySequence !== priorById) {
      byId.delete(priorBySequence.id);
    }
    if (priorById && priorById.seq !== event.seq) {
      bySequence.delete(priorById.seq);
    }
    bySequence.set(event.seq, event);
    byId.set(event.id, event);
  }
  return [...bySequence.values()].sort((a, b) => a.seq - b.seq);
}
