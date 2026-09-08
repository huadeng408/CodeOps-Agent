export interface Session {
  id: string;
  userId: number;
  projectName: string;
  title: string;
  goal: string;
  status: 'queued' | 'running' | 'paused' | 'done';
  eventCount: number;
  createdAt: string;
  updatedAt: string;
  run?: SessionRun;
  lastUserInput?: string;
}

export type SessionRunStatus = 'queued' | 'running' | 'completed' | 'failed';

export interface SessionRun {
  sessionId: string;
  runId: string;
  requestId: string;
  status: SessionRunStatus;
  attempt: number;
  workerId?: string;
  checkpointHash: string;
  leaseUntil?: string;
  startedAt?: string;
  completedAt?: string;
  error?: string;
  retryOfRunId?: string;
  retryOfRunIds?: string[];
}

export interface SessionEvent {
  id: string;
  sessionId: string;
  type: string;
  author: string;
  content: string;
  toolName?: string;
  toolStatus?: string;
  toolOutput?: string;
  hash: string;
  prevHash: string;
  seq: number;
  createdAt: string;
  rewindTargetSeq?: number;
  continuation?: {
    checkpointHash: string;
    targetEventId: string;
    targetSeq: number;
    targetHash: string;
    resumeCount: number;
  };
}

export interface SessionCheckpoint {
  id: string;
  sessionId: string;
  eventId: string;
  hash: string;
  targetHash: string;
  seq: number;
  label: string;
  createdAt: string;
}

export interface RecoveryManifest {
  session: Session;
  ledgerSeq: number;
  latestLedgerHash: string;
  eventCount: number;
  rewindCount: number;
  continuationCount: number;
  checkpointCount: number;
  latestRecovery?: {
    type: 'session/rewind' | 'session/continued';
    seq: number;
    targetSeq?: number;
    resumeCount?: number;
  };
  currentRun?: {
    runId: string;
    status: SessionRunStatus;
    attempt: number;
    retryOfRunId?: string;
    retryOfRunIds?: string[];
  };
  runs?: Array<{
    runId: string;
    status: SessionRunStatus;
    attempt: number;
    retryOfRunId?: string;
    retryOfRunIds?: string[];
  }>;
}

export interface WorkspaceManifest {
  available: boolean;
  reason?: string;
  worktrees?: WorkspaceWorktree[];
}

export interface WorkspaceWorktree {
  name: string;
  baseRef?: string;
  status?: string;
  active?: boolean;
  diffLines?: string[];
  diffError?: string;
}

export interface ApiResponse<T> {
  code: number;
  message: string;
  data: T;
}
