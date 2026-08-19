/**
 * Template API implementation
 */
import { API_ENDPOINTS, DEFAULT_PAGINATION } from '@/constants/api';
import { get, post, put, del } from '@/utils/http/request';
import type {
  TemplateResponse,
  CreateTemplateRequest,
  UpdateTemplateRequest,
} from './types';

/**
 * Get template list
 */
export function listTemplates(params?: { limit?: number; offset?: number }): Promise<TemplateResponse[]> {
  return get<TemplateResponse[]>(API_ENDPOINTS.TEMPLATES, {
    params: { ...DEFAULT_PAGINATION, ...params },
  });
}

/**
 * Get template details
 */
export function getTemplate(templateId: string): Promise<TemplateResponse> {
  return get<TemplateResponse>(API_ENDPOINTS.TEMPLATE(templateId));
}

/**
 * Create template
 */
export function createTemplate(data: CreateTemplateRequest): Promise<TemplateResponse> {
  return post<TemplateResponse>(API_ENDPOINTS.TEMPLATES, data);
}

/**
 * Update template
 */
export function updateTemplate(templateId: string, data: UpdateTemplateRequest): Promise<TemplateResponse> {
  return put<TemplateResponse>(API_ENDPOINTS.TEMPLATE(templateId), data);
}

/**
 * Delete template
 */
export function deleteTemplate(templateId: string): Promise<{ message: string }> {
  return del<{ message: string }>(API_ENDPOINTS.TEMPLATE(templateId));
}
