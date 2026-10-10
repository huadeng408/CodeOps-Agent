export interface Session {
  id: string;
  userId: number;
  projectName: string;
  title: string;
  goal: string;
  workingDir: string;
  status: 'queued' | 'running' | 'paused' | 'done';
  eventCount: number;
  createdAt: string;
  updatedAt: string;
  run?: SessionRun;
  planTodo?: PlanTodo;
}

export interface TaskWorkspace {
  available: boolean;
  state: 'unprepared' | 'prepared' | 'blocked' | 'unknown';
  reason?: string;
  repository: string;
  workspaceId?: string;
  leaseExpiresAt?: string;
  eventCount: number;
  baseline?: {
    repository_id: string;
    head_commit: string;
    checksum: string;
    files: Array<{ path: string; exists: boolean; sha256?: string; size: number; mode: number }>;
    excluded: Array<{ path: string; reason: string }>;
  };
}

export interface PlanTodo {
  revision: number;
  plan: {
    steps?: string[];
    currentIndex: number;
    mode?: string;
  };
  todos: Array<{
    content: string;
    activeForm?: string;
    status: 'pending' | 'in_progress' | 'completed' | string;
  }>;
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
    requestId?: string;
    runId?: string;
  };
	approval?: {
		runId: string;
		toolCallId: string;
		toolName: string;
		argumentsJson: string;
		decision: 'pending' | 'approved' | 'denied';
	};
	codeModification?: {
		runId: string;
		toolCallId: string;
		toolName: string;
		path: string;
		operation: string;
		summary: string;
		beforeSha256: string;
		afterSha256: string;
		diffSha256: string;
	};
  progress?: {
    runId: string;
    kind: 'phase' | 'milestone' | 'narration' | string;
    title: string;
    summary: string;
    planRevision?: number;
    todoRevision?: number;
    sourceEventSeq: number;
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

export interface ContinuationRuntimeStatus {
  attached: boolean;
  generation: number;
  last_health_error?: string;
  last_recovery_at?: string;
  last_transition_at?: string;
  consecutive_failures: number;
  next_retry_at?: string;
}

export interface CapabilityStatus {
  state: 'ready' | 'blocked' | 'degraded' | 'unknown';
  reason?: string;
  tokens?: { batch_id?: string; limit: number; used: number; reserved: number; cost_status: string };
}

export type RuntimeCapabilities = Partial<Record<'identity' | 'history' | 'provider' | 'sandbox' | 'budget' | 'execution' | 'rag' | 'trace', CapabilityStatus>>;
