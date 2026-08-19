/**
 * Health API implementation
 */
import { API_ENDPOINTS } from '@/constants/api';
import { get, post } from '@/utils/http/request';
import type { HealthResponse } from '@/types/api';

/**
 * Health Check
 */
export function healthCheck(): Promise<HealthResponse> {
  return get<HealthResponse>(API_ENDPOINTS.HEALTH);
}

/**
 * Detailed health check
 */
export function detailedHealthCheck(): Promise<Record<string, unknown>> {
  return get<Record<string, unknown>>(API_ENDPOINTS.HEALTH_DETAILED);
}

/**
 * Manually trigger state synchronization
 */
export function triggerStateSync(): Promise<Record<string, unknown>> {
  return post<Record<string, unknown>>(API_ENDPOINTS.HEALTH + '/sync', {});
}
