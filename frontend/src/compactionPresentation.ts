export interface CompactionPresentation {
  kind: 'progress';
  title: string;
  summary: string;
}

interface CompactionEventLike {
  type: string;
  content: string;
}

export function isCompactionEvent(event: CompactionEventLike): boolean {
  return event.type === 'context/compaction';
}

export function compactionPresentation(event: CompactionEventLike): CompactionPresentation | null {
  if (!isCompactionEvent(event)) return null;
  const content = event.content.trim();
  const bounded = content.length > 180 ? content.slice(0, 177) + '…' : content;
  return {
    kind: 'progress',
    title: '整理上下文',
    summary: bounded ? '已整理较早对话，' + bounded : '已整理较早对话，保留当前任务所需上下文',
  };
}
