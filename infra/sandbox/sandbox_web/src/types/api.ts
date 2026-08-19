/**
 * API type definitions
 * Based on the Control Plane OpenAPI specification
 */

// ============================================
// Common types
// ============================================

/** Runtime type */
export type RuntimeType = 'python3.11' | 'nodejs20' | 'java17' | 'go1.21';

/** Session status */
export type SessionStatus = 'PENDING' | 'CREATING' | 'STARTING' | 'RUNNING' | 'COMPLETED' | 'TERMINATED' | 'FAILED' | 'TIMEOUT';

/** Dependency installation status */
export type DependencyInstallStatus = 'pending' | 'installing' | 'completed' | 'failed';

/** Execution status */
export type ExecutionStatus = 'PENDING' | 'RUNNING' | 'COMPLETED' | 'FAILED' | 'TIMEOUT' | 'CRASHED';

/** Programming language */
export type CodeLanguage = 'python' | 'javascript' | 'shell';

// ============================================
// Template-related types
// ============================================

/** Template response */
export interface TemplateResponse {
  id: string;
  name: string;
  image_url: string;
  runtime_type: RuntimeType;
  default_cpu_cores: number;
  default_memory_mb: number;
  default_disk_mb: number;
  default_timeout_sec: number;
  default_env_vars?: Record<string, string>;
  is_active: boolean;
  created_at?: string;
  updated_at?: string;
}

/** Create template request */
export interface CreateTemplateRequest {
  id: string;
  name: string;
  image_url: string;
  runtime_type: RuntimeType;
  default_cpu_cores?: number;
  default_memory_mb?: number;
  default_disk_mb?: number;
  default_timeout?: number;
  default_env_vars?: Record<string, string>;
}

/** Update template request */
export interface UpdateTemplateRequest {
  name?: string;
  image_url?: string;
  default_cpu_cores?: number;
  default_memory_mb?: number;
  default_disk_mb?: number;
  default_timeout?: number;
  default_env_vars?: Record<string, string>;
}

// ============================================
// Session-related types
// ============================================

/** Resource limit response */
export interface ResourceLimitResponse {
  cpu: string;
  memory: string;
  disk: string;
  max_processes?: number;
}

/** Session response */
export interface SessionResponse {
  id: string;
  template_id: string;
  status: SessionStatus;
  resource_limit?: ResourceLimitResponse;
  workspace_path?: string;
  runtime_type: RuntimeType;
  runtime_node?: string;
  container_id?: string;
  pod_name?: string;
  env_vars: Record<string, string>;
  timeout: number;
  python_package_index_url?: string;
  requested_dependencies?: DependencySpec[];
  installed_dependencies?: InstalledDependencyResponse[];
  dependency_install_status?: DependencyInstallStatus;
  dependency_install_error?: string | null;
  dependency_install_started_at?: string | null;
  dependency_install_completed_at?: string | null;
  created_at: string;
  updated_at: string;
  completed_at?: string;
  last_activity_at?: string;
}

/** Dependency spec */
export interface DependencySpec {
  name: string;
  version?: string;
}

/** Installed dependency */
export interface InstalledDependencyResponse {
  name: string;
  version: string;
  install_location: string;
  install_time: string;
  is_from_template?: boolean;
}

/** Create session request */
export interface CreateSessionRequest {
  template_id?: string;
  timeout?: number;
  cpu?: string;
  memory?: string;
  disk?: string;
  env_vars?: Record<string, string>;
  event?: Record<string, unknown>;
  python_package_index_url?: string;
  dependencies?: DependencySpec[];
  install_timeout?: number;
  fail_on_dependency_error?: boolean;
  allow_version_conflicts?: boolean;
}

/** Install session dependencies request */
export interface InstallSessionDependenciesRequest {
  python_package_index_url?: string;
  dependencies: DependencySpec[];
}

/** sessionlistresponse */
export interface SessionListResponse {
  items: SessionResponse[];
  total: number;
  limit: number;
  offset: number;
}

/** Session list query parameters */
export interface ListSessionsParams {
  status?: SessionStatus | null;
  template_id?: string | null;
  limit?: number;
  offset?: number;
}

// ============================================
// Execution-related types
// ============================================

/** fileartifactresponse */
export interface ArtifactResponse {
  path: string;
  size: number;
  mime_type: string;
  type: string;
  created_at: string;
  checksum?: string;
}

/** Execution metrics */
export interface ExecutionMetrics {
  duration_ms: number;
  cpu_time_ms?: number;
  peak_memory_mb?: number;
  io_read_bytes?: number;
  io_write_bytes?: number;
}

/** Execution response */
export interface ExecutionResponse {
  id: string;
  session_id: string;
  status: ExecutionStatus;
  code?: string;
  language?: CodeLanguage;
  timeout?: number;
  exit_code?: number;
  error_message?: string;
  execution_time?: number;
  stdout?: string;
  stderr?: string;
  artifacts: ArtifactResponse[];
  retry_count: number;
  created_at?: string;
  started_at?: string;
  completed_at?: string;
  return_value?: Record<string, unknown>;
  metrics?: ExecutionMetrics;
}

/** executioncoderequest */
export interface ExecuteCodeRequest {
  code: string;
  language: CodeLanguage;
  timeout?: number;
  event?: Record<string, unknown>;
  working_directory?: string;
}

/** executioncoderesponse */
export interface ExecuteCodeResponse {
  execution_id: string;
  session_id: string;
  status: ExecutionStatus;
  created_at?: string;
}

/** File upload response */
export interface FileUploadResponse {
  session_id: string;
  mode: 'file' | 'archive_extract';
  file_path?: string | null;
  extract_path?: string | null;
  extracted_file_count?: number | null;
  skipped_file_count?: number | null;
  size: number;
}

/** executionlistresponse */
export interface ExecutionListResponse {
  items: ExecutionResponse[];
  total: number;
  limit: number;
  offset: number;
}

// ============================================
// Health-related types
// ============================================

/** Health Checkresponse */
export interface HealthResponse {
  status: string;
  version: string;
  uptime: number;
}

/** Detailed health checkresponse */
export interface DetailedHealthResponse extends Record<string, unknown> {
  status: string;
  version?: string;
  uptime?: number;
  dependencies?: Record<string, string>;
}

// ============================================
// File-related types
// ============================================

/** File upload response */
export interface FileUploadResponse {
  session_id: string;
  file_path: string;
  size: number;
}

// ============================================
// Common API response types
// ============================================

/** API errorresponse */
export interface ErrorResponse {
  detail: Array<{
    loc: Array<string | number>;
    msg: string;
    type: string;
  }>;
}

/** Pagination parameters */
export interface PaginationParams {
  limit?: number;
  offset?: number;
}
