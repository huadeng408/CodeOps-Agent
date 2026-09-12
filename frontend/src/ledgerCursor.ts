export function persistedCursor(sessionId: string, fallback: number): number {
  let persisted = fallback;
  try {
    const raw = localStorage.getItem("codeops:ledger-cursor:" + sessionId);
    if (raw !== null) {
      const cursor = Number.parseInt(raw, 10);
      if (Number.isSafeInteger(cursor) && cursor >= -1) persisted = cursor;
    }
  } catch {
    // Browser storage is advisory; the in-memory cursor remains usable.
  }
  return Math.max(fallback, persisted);
}
