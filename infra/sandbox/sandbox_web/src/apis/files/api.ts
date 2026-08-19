/**
 * File API implementation
 */
import { API_ENDPOINTS } from '@/constants/api';
import apiClient from '@/utils/http/axios';
import type { FileUploadResponse } from './types';

/**
 * Upload a file to the session workspace
 * @param sessionId Session ID
 * @param file File to upload
 * @param path File path in the session workspace (as a URL parameter; defaults to the file name)
 */
export async function uploadFile(
  sessionId: string,
  file: File,
  path?: string,
  options?: {
    extract?: boolean;
    overwrite?: boolean;
  }
): Promise<FileUploadResponse> {
  const formData = new FormData();
  formData.append('file', file);

  // path is required; use the file name if it is not provided
  const uploadPath = path || file.name;

  const response = await apiClient.post<FileUploadResponse>(
    API_ENDPOINTS.UPLOAD_FILE(sessionId),
    formData,
    {
      params: {
        path: uploadPath,
        extract: options?.extract ?? false,
        overwrite: options?.overwrite ?? false,
      },
      headers: {
        // Override the default application/json so axios can detect FormData and set the correct Content-Type
        'Content-Type': undefined as any,
      },
    }
  );
  return response;
}

/**
 * Download a file from the session workspace
 */
export async function downloadFile(sessionId: string, filePath: string): Promise<Blob> {
  const response = await apiClient.get<Blob>(
    API_ENDPOINTS.DOWNLOAD_FILE(sessionId, filePath),
    {
      responseType: 'blob',
    }
  );
  return response;
}
