import type { SessionEvent } from './types';

function isProgressEvent(event: SessionEvent): boolean {
  return Boolean(event.progress) || event.type === 'session/progress' || event.type === 'context/compaction';
}

/** Associate ledger progress with the closest preceding user turn. */
export function groupProgressByMessage(events: SessionEvent[]): Map<string, SessionEvent[]> {
  const grouped = new Map<string, SessionEvent[]>();
  const messages = events
    .filter((event) => event.type === 'user/message')
    .sort((a, b) => a.seq - b.seq);
  const progressEvents = events.filter(isProgressEvent);
  messages.forEach((message, index) => {
    const nextMessageSeq = messages[index + 1]?.seq ?? Number.POSITIVE_INFINITY;
    const items = progressEvents.filter((progress) => progress.seq > message.seq && progress.seq < nextMessageSeq);
    if (items.length > 0) grouped.set(message.id, items.sort((a, b) => a.seq - b.seq));
  });
  return grouped;
}

/** Keep the ledger intact while avoiding a second visual copy of nested progress. */
export function hideGroupedProgress(events: SessionEvent[], grouped: Map<string, SessionEvent[]>): SessionEvent[] {
  const groupedProgressIds = new Set([...grouped.values()].flat().map((event) => event.id));
  return events.filter((event) => !groupedProgressIds.has(event.id));
}
