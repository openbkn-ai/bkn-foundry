/**
 * Runtime type constants
 */

import type { RuntimeType, CodeLanguage, SessionStatus, ExecutionStatus } from '@/types/api';

/** Supported runtime types */
export const RUNTIME_TYPES: readonly RuntimeType[] = ['python3.11', 'nodejs20', 'java17', 'go1.21'] as const;

/** Runtime type display-name mapping */
export const RUNTIME_TYPE_LABELS: Record<RuntimeType, string> = {
  'python3.11': 'Python 3.11',
  'nodejs20': 'Node.js 20',
  'java17': 'Java 17',
  'go1.21': 'Go 1.21',
} as const;

/** Supported programming languages */
export const CODE_LANGUAGES: readonly CodeLanguage[] = ['python', 'javascript', 'shell'] as const;

/** Programming language display-name mapping */
export const CODE_LANGUAGE_LABELS: Record<CodeLanguage, string> = {
  python: 'Python',
  javascript: 'JavaScript',
  shell: 'Shell',
} as const;

/** Session status display-name mapping */
export const SESSION_STATUS_LABELS: Record<SessionStatus, string> = {
  PENDING: '等待中',
  CREATING: '启动中',
  STARTING: '启动中',
  RUNNING: '运行中',
  COMPLETED: '已完成',
  TERMINATED: '已终止',
  FAILED: '失败',
  TIMEOUT: '超时',
} as const;

/** Execution status display-name mapping */
export const EXECUTION_STATUS_LABELS: Record<ExecutionStatus, string> = {
  PENDING: '等待中',
  RUNNING: '执行中',
  COMPLETED: '成功',
  FAILED: '失败',
  TIMEOUT: '超时',
  CRASHED: '崩溃',
} as const;

/** Resource configuration options */
export const RESOURCE_OPTIONS = {
  CPU: ['0.5', '1', '2', '4'] as const,
  MEMORY: ['128Mi', '256Mi', '512Mi', '1Gi', '2Gi', '4Gi', '8Gi'] as const,
  DISK: ['256Mi', '512Mi', '1Gi', '5Gi', '10Gi', '20Gi', '50Gi'] as const,
} as const;
