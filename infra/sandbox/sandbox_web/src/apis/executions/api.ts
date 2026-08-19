/**
 * Execution API implementation
 */
import { API_ENDPOINTS, DEFAULT_PAGINATION } from '@/constants/api';
import { get, post } from '@/utils/http/request';
import type {
  ExecutionResponse,
  ExecuteCodeRequest,
  ExecuteCodeResponse,
  ExecutionListResponse,
} from './types';

/**
 * Submit code execution
 */
export function executeCode(sessionId: string, data: ExecuteCodeRequest): Promise<ExecuteCodeResponse> {
  return post<ExecuteCodeResponse>(API_ENDPOINTS.EXECUTE(sessionId), data);
}

/**
 * Get execution status
 */
export function getExecutionStatus(executionId: string): Promise<ExecutionResponse> {
  return get<ExecutionResponse>(API_ENDPOINTS.EXECUTION_STATUS(executionId));
}

/**
 * Get execution result
 */
export function getExecutionResult(executionId: string): Promise<ExecutionResponse> {
  return get<ExecutionResponse>(API_ENDPOINTS.EXECUTION_RESULT(executionId));
}

/**
 * Get the execution list for a session
 */
export function listSessionExecutions(
  sessionId: string,
  params?: { limit?: number; offset?: number }
): Promise<ExecutionListResponse> {
  return get<ExecutionListResponse>(API_ENDPOINTS.EXECUTIONS_BY_SESSION(sessionId), {
    params: { ...DEFAULT_PAGINATION, ...params },
  });
}
