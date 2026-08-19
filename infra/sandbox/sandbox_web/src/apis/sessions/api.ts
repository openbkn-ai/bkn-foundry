/**
 * Session API implementation
 */
import { API_ENDPOINTS, LONG_TASK_HTTP_TIMEOUT } from '@/constants/api';
import { get, post, del } from '@/utils/http/request';
import type {
  SessionResponse,
  CreateSessionRequest,
  InstallSessionDependenciesRequest,
  SessionListResponse,
  ListSessionsParams,
} from './types';

/**
 * Get session list
 */
export function listSessions(params?: ListSessionsParams): Promise<SessionListResponse> {
  return get<SessionListResponse>(API_ENDPOINTS.SESSIONS, { params });
}

/**
 * Get session details
 */
export function getSession(sessionId: string): Promise<SessionResponse> {
  return get<SessionResponse>(API_ENDPOINTS.SESSION(sessionId));
}

/**
 * createsession
 */
export function createSession(data: CreateSessionRequest): Promise<SessionResponse> {
  return post<SessionResponse>(API_ENDPOINTS.SESSIONS, data, {
    timeout: LONG_TASK_HTTP_TIMEOUT,
  });
}

/**
 * Install session dependencies
 */
export function installSessionDependencies(
  sessionId: string,
  data: InstallSessionDependenciesRequest,
): Promise<SessionResponse> {
  return post<SessionResponse>(API_ENDPOINTS.INSTALL_SESSION_DEPENDENCIES(sessionId), data, {
    timeout: LONG_TASK_HTTP_TIMEOUT,
  });
}

/**
 * Terminate session
 */
export function terminateSession(sessionId: string): Promise<SessionResponse> {
  return del<SessionResponse>(API_ENDPOINTS.SESSION(sessionId));
}
