/**
 * API-related constants
 */

export { getApiBaseUrl } from '@/utils/config';

/** API endpoint paths */
export const API_ENDPOINTS = {
  // Health
  HEALTH: '/api/v1/health',
  HEALTH_DETAILED: '/api/v1/health/detailed',

  // Templates
  TEMPLATES: '/api/v1/templates',
  TEMPLATE: (id: string) => `/api/v1/templates/${id}`,

  // Sessions
  SESSIONS: '/api/v1/sessions',
  SESSION: (id: string) => `/api/v1/sessions/${id}`,
  INSTALL_SESSION_DEPENDENCIES: (id: string) =>
    `/api/v1/sessions/${id}/dependencies/install`,

  // Executions
  EXECUTE: (sessionId: string) => `/api/v1/executions/sessions/${sessionId}/execute`,
  EXECUTION_STATUS: (id: string) => `/api/v1/executions/${id}/status`,
  EXECUTION_RESULT: (id: string) => `/api/v1/executions/${id}/result`,
  EXECUTIONS_BY_SESSION: (sessionId: string) =>
    `/api/v1/executions/sessions/${sessionId}/executions`,

  // Files
  UPLOAD_FILE: (sessionId: string) => `/api/v1/sessions/${sessionId}/files/upload`,
  DOWNLOAD_FILE: (sessionId: string, filePath: string) =>
    `/api/v1/sessions/${sessionId}/files/${filePath}`,
} as const;

/** Default pagination parameters */
export const DEFAULT_PAGINATION = {
  LIMIT: 50,
  OFFSET: 0,
} as const;

/** HTTP timeout (milliseconds) */
export const HTTP_TIMEOUT = 30000;

/** Long-running HTTP timeout (milliseconds) */
export const LONG_TASK_HTTP_TIMEOUT = 180000;
