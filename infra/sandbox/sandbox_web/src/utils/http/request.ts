/**
 * HTTP request wrapper utilities
 */
import apiClient from './axios';
import type { AxiosRequestConfig } from 'axios';

/**
 * Generic GET request
 */
export function get<T = unknown>(url: string, config?: AxiosRequestConfig): Promise<T> {
  return apiClient.get<T>(url, config);
}

/**
 * Generic POST request
 */
export function post<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<T> {
  return apiClient.post<T>(url, data, config);
}

/**
 * Generic PUT request
 */
export function put<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<T> {
  return apiClient.put<T>(url, data, config);
}

/**
 * Generic DELETE request
 */
export function del<T = unknown>(url: string, config?: AxiosRequestConfig): Promise<T> {
  return apiClient.delete<T>(url, config);
}

/**
 * Generic PATCH request
 */
export function patch<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<T> {
  return apiClient.patch<T>(url, data, config);
}
