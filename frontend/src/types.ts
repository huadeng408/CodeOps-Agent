export interface Session {
  id: string;
  userId: number;
  projectName: string;
  title: string;
  goal: string;
  status: 'running' | 'paused' | 'done';
  eventCount: number;
  createdAt: string;
  updatedAt: string;
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

export interface ApiResponse<T> {
  code: number;
  message: string;
  data: T;
}
