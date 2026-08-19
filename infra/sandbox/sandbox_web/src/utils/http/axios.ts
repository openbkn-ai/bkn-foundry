/**
 * Axios instance configuration
 */
import axios, { type AxiosInstance, type AxiosRequestConfig, type AxiosResponse } from 'axios';
import { getApiBaseUrl, HTTP_TIMEOUT } from '@/constants/api';

/** Create an API client instance */
export const apiClient: AxiosInstance = axios.create({
  baseURL: getApiBaseUrl(),
  timeout: HTTP_TIMEOUT,
  headers: {
    'Content-Type': 'application/json',
  },
});

/** Request interceptor */
apiClient.interceptors.request.use(
  (config) => {
    // Dynamically set baseURL from runtime config
    config.baseURL = getApiBaseUrl();

    // Debug logging for API URL detection (can be disabled in production)
    if (import.meta.env.DEV) {
      console.debug(`[API] Request to: ${config.baseURL}${config.url}`);
    }

    // Authentication tokens can be added here
    // const token = localStorage.getItem('token');
    // if (token) {
    //   config.headers.Authorization = `Bearer ${token}`;
    // }
    return config;
  },
  (error) => {
    return Promise.reject(error);
  }
);

/** Response interceptor */
apiClient.interceptors.response.use(
  (response: AxiosResponse) => {
    // Return response data directly
    return response.data;
  },
  (error) => {
    // Unified error handling
    if (error.response) {
      // Server returned an error status code
      const { status, data } = error.response;
      console.error(`API Error [${status}]:`, data);
    } else if (error.request) {
      // Request was sent but no response was received
      console.error('Network Error:', error.message);
    } else {
      // Request configuration error
      console.error('Request Error:', error.message);
    }
    return Promise.reject(error);
  }
);

export default apiClient;
