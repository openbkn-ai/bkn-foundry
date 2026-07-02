package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
)

const (
	sandboxHealthHealthy   = "healthy"
	sandboxHealthDegraded  = "degraded"
	sandboxHealthUnhealthy = "unhealthy"
	defaultSessionSource   = "unknown"
)

type PoolSnapshot struct {
	MaxSessions           int
	ActiveSessions        int
	MaxConcurrentTasks    int
	CurrentActiveSessions int
	CurrentRunningTasks   int
	TemplateID            string
	SessionResources      config.SessionResourcesConfig
	Sessions              []PoolSessionSnapshot
}

type PoolSessionSnapshot struct {
	ID           string
	RunningTasks int
	LastUsedAt   time.Time
}

type SandboxManagementService interface {
	GetHealth(ctx context.Context) (*SandboxHealthResp, error)
	GetPool(ctx context.Context) (*SandboxPoolResp, error)
	ListSessions(ctx context.Context, req *SandboxSessionListReq) (*SandboxSessionListResp, error)
	GetSessionDetail(ctx context.Context, sessionID string) (*SandboxSessionDetailResp, error)
}

type SandboxHealthResp struct {
	Status                string `json:"status"`
	ControlPlaneReachable bool   `json:"control_plane_reachable"`
	CheckedAt             string `json:"checked_at"`
	MaxSessions           int    `json:"max_sessions"`
	CurrentActiveSessions int    `json:"current_active_sessions"`
	CurrentRunningTasks   int    `json:"current_running_tasks"`
	FailedSessions        int    `json:"failed_sessions"`
	Message               string `json:"message,omitempty"`
}

type SandboxPoolResp struct {
	MaxSessions           int                           `json:"max_sessions"`
	ActiveSessions        int                           `json:"active_sessions"`
	MaxConcurrentTasks    int                           `json:"max_concurrent_tasks"`
	CurrentActiveSessions int                           `json:"current_active_sessions"`
	CurrentRunningTasks   int                           `json:"current_running_tasks"`
	TemplateID            string                        `json:"template_id"`
	SessionResources      config.SessionResourcesConfig `json:"session_resources"`
	Sessions              []PoolSessionSnapshotResp     `json:"sessions"`
}

type PoolSessionSnapshotResp struct {
	ID           string `json:"id"`
	RunningTasks int    `json:"running_tasks"`
	LastUsedAt   string `json:"last_used_at,omitempty"`
}

type SandboxSessionListReq struct {
	Limit        int                      `form:"limit"`
	Offset       int                      `form:"offset"`
	Status       interfaces.SessionStatus `form:"status"`
	Source       string                   `form:"source"`
	Runtime      string                   `form:"runtime"`
	AbnormalOnly bool                     `form:"abnormal_only"`
}

type SandboxSessionListResp struct {
	Items   []*SandboxSessionSummary `json:"items"`
	Total   int                      `json:"total"`
	Limit   int                      `json:"limit"`
	Offset  int                      `json:"offset"`
	HasMore bool                     `json:"has_more"`
}

type SandboxSessionSummary struct {
	ID                      string                   `json:"id"`
	Status                  interfaces.SessionStatus `json:"status"`
	Source                  string                   `json:"source"`
	TaskID                  string                   `json:"task_id,omitempty"`
	CapabilityID            string                   `json:"capability_id,omitempty"`
	CapabilityName          string                   `json:"capability_name,omitempty"`
	UserID                  string                   `json:"user_id,omitempty"`
	UserName                string                   `json:"user_name,omitempty"`
	TemplateID              string                   `json:"template_id"`
	RuntimeType             string                   `json:"runtime_type"`
	LanguageRuntime         string                   `json:"language_runtime,omitempty"`
	ResourceLimit           map[string]any           `json:"resource_limit,omitempty"`
	DependencyInstallStatus string                   `json:"dependency_install_status,omitempty"`
	RecentErrorSummary      string                   `json:"recent_error_summary,omitempty"`
	CreatedAt               string                   `json:"created_at,omitempty"`
	UpdatedAt               string                   `json:"updated_at,omitempty"`
	LastActivityAt          string                   `json:"last_activity_at,omitempty"`
}

type SandboxSessionDetailResp struct {
	*SandboxSessionSummary
	WorkspacePath                string                       `json:"workspace_path,omitempty"`
	RuntimeNode                  string                       `json:"runtime_node,omitempty"`
	PodName                      string                       `json:"pod_name,omitempty"`
	Timeout                      int                          `json:"timeout,omitempty"`
	PythonPackageIndexURL        string                       `json:"python_package_index_url,omitempty"`
	RequestedDependencies        []*interfaces.DependencyInfo `json:"requested_dependencies,omitempty"`
	InstalledDependencies        []*interfaces.DependencyInfo `json:"installed_dependencies,omitempty"`
	DependencyInstallStartedAt   string                       `json:"dependency_install_started_at,omitempty"`
	DependencyInstallCompletedAt string                       `json:"dependency_install_completed_at,omitempty"`
	FullStdoutStderrAvailable    bool                         `json:"full_stdout_stderr_available"`
	GovernanceActionsAvailable   bool                         `json:"governance_actions_available"`
	SensitiveDiagnosticsRedacted bool                         `json:"sensitive_diagnostics_redacted"`
}

type sandboxManagementService struct {
	client interfaces.SandBoxControlPlane
	pool   interface {
		Snapshot() PoolSnapshot
	}
}

func NewSandboxManagementService(client interfaces.SandBoxControlPlane, pool interface {
	Snapshot() PoolSnapshot
}) SandboxManagementService {
	return &sandboxManagementService{client: client, pool: pool}
}

func (s *sandboxManagementService) GetHealth(ctx context.Context) (*SandboxHealthResp, error) {
	snapshot := s.pool.Snapshot()
	limit := snapshot.MaxSessions
	if limit <= 0 {
		limit = defaultMaxSessions
	}
	resp, err := s.client.ListSessions(ctx, &interfaces.ListSessionsReq{Limit: limit})
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	health := &SandboxHealthResp{
		Status:                sandboxHealthHealthy,
		ControlPlaneReachable: true,
		CheckedAt:             checkedAt,
		MaxSessions:           snapshot.MaxSessions,
		CurrentActiveSessions: snapshot.CurrentActiveSessions,
		CurrentRunningTasks:   snapshot.CurrentRunningTasks,
	}
	if err != nil {
		health.Status = sandboxHealthUnhealthy
		health.ControlPlaneReachable = false
		health.Message = err.Error()
		return health, nil
	}
	if resp != nil {
		for _, item := range resp.Sessions {
			if isAbnormalSession(item) {
				health.FailedSessions++
			}
		}
	}
	if health.FailedSessions > 0 {
		health.Status = sandboxHealthDegraded
	}
	return health, nil
}

func (s *sandboxManagementService) GetPool(ctx context.Context) (*SandboxPoolResp, error) {
	snapshot := s.pool.Snapshot()
	items := make([]PoolSessionSnapshotResp, 0, len(snapshot.Sessions))
	for _, item := range snapshot.Sessions {
		respItem := PoolSessionSnapshotResp{
			ID:           item.ID,
			RunningTasks: item.RunningTasks,
		}
		if !item.LastUsedAt.IsZero() {
			respItem.LastUsedAt = item.LastUsedAt.UTC().Format(time.RFC3339)
		}
		items = append(items, respItem)
	}
	return &SandboxPoolResp{
		MaxSessions:           snapshot.MaxSessions,
		ActiveSessions:        snapshot.ActiveSessions,
		MaxConcurrentTasks:    snapshot.MaxConcurrentTasks,
		CurrentActiveSessions: snapshot.CurrentActiveSessions,
		CurrentRunningTasks:   snapshot.CurrentRunningTasks,
		TemplateID:            snapshot.TemplateID,
		SessionResources:      snapshot.SessionResources,
		Sessions:              items,
	}, nil
}

func (s *sandboxManagementService) ListSessions(ctx context.Context, req *SandboxSessionListReq) (*SandboxSessionListResp, error) {
	if req == nil {
		req = &SandboxSessionListReq{}
	}
	resp, err := s.client.ListSessions(ctx, &interfaces.ListSessionsReq{
		Limit:  req.Limit,
		Offset: req.Offset,
		Status: req.Status,
	})
	if err != nil {
		return nil, err
	}
	result := &SandboxSessionListResp{
		Items:   []*SandboxSessionSummary{},
		Limit:   req.Limit,
		Offset:  req.Offset,
		HasMore: false,
	}
	if resp == nil {
		return result, nil
	}
	result.Limit = resp.Limit
	result.Offset = resp.Offset
	result.HasMore = resp.HasMore
	for _, item := range resp.Sessions {
		summary := newSandboxSessionSummary(item)
		if !matchSessionFilters(summary, req) {
			continue
		}
		result.Items = append(result.Items, summary)
	}
	result.Total = len(result.Items)
	return result, nil
}

func (s *sandboxManagementService) GetSessionDetail(ctx context.Context, sessionID string) (*SandboxSessionDetailResp, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.DefaultHTTPError(ctx, http.StatusBadRequest, "session_id is required")
	}
	exists, detail, err := s.client.QuerySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !exists || detail == nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("session %s not found", sessionID))
	}
	summary := newSandboxSessionSummary(detail)
	return &SandboxSessionDetailResp{
		SandboxSessionSummary:        summary,
		WorkspacePath:                detail.WorkspacePath,
		RuntimeNode:                  detail.RuntimeNode,
		PodName:                      detail.PodName,
		Timeout:                      detail.Timeout,
		PythonPackageIndexURL:        detail.PythonPackageIndexURL,
		RequestedDependencies:        detail.RequestedDependencies,
		InstalledDependencies:        detail.InstalledDependencies,
		DependencyInstallStartedAt:   detail.DependencyInstallStartedAt,
		DependencyInstallCompletedAt: detail.DependencyInstallCompletedAt,
		FullStdoutStderrAvailable:    false,
		GovernanceActionsAvailable:   false,
		SensitiveDiagnosticsRedacted: true,
	}, nil
}

func (p *sessionPoolImpl) Snapshot() PoolSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	sessions := make([]PoolSessionSnapshot, 0, len(p.sessions))
	runningTasks := 0
	for _, item := range p.sessions {
		if item == nil {
			continue
		}
		runningTasks += item.RunningTasks
		sessions = append(sessions, PoolSessionSnapshot{
			ID:           item.ID,
			RunningTasks: item.RunningTasks,
			LastUsedAt:   item.LastUsedAt,
		})
	}
	return PoolSnapshot{
		MaxSessions:           p.maxSessions,
		ActiveSessions:        p.activeSessions,
		MaxConcurrentTasks:    p.maxConcurrentTasks,
		CurrentActiveSessions: len(p.sessions),
		CurrentRunningTasks:   runningTasks,
		TemplateID:            p.templateID,
		SessionResources:      p.reqConfig,
		Sessions:              sessions,
	}
}

func newSandboxSessionSummary(detail *interfaces.SessionDetail) *SandboxSessionSummary {
	if detail == nil {
		return &SandboxSessionSummary{Source: defaultSessionSource}
	}
	return &SandboxSessionSummary{
		ID:                      detail.ID,
		Status:                  detail.Status,
		Source:                  detectSessionSource(detail),
		TaskID:                  readSessionEnvString(detail, "task_id", "taskId", "execution_task_id"),
		CapabilityID:            readSessionEnvString(detail, "capability_id", "capabilityId", "source_capability_id"),
		CapabilityName:          readSessionEnvString(detail, "capability_name", "capabilityName", "source_capability_name"),
		UserID:                  readSessionEnvString(detail, "user_id", "userId", "created_by", "operator_user_id"),
		UserName:                readSessionEnvString(detail, "user_name", "userName", "created_by_name", "operator_user_name"),
		TemplateID:              detail.TemplateID,
		RuntimeType:             detail.RuntimeType,
		LanguageRuntime:         detail.LanguageRuntime,
		ResourceLimit:           detail.ResourceLimit,
		DependencyInstallStatus: detail.DependencyInstallStatus,
		RecentErrorSummary:      summarizeSessionError(detail),
		CreatedAt:               detail.CreateAt,
		UpdatedAt:               detail.UpdateAt,
		LastActivityAt:          detail.LastActivityAt,
	}
}

func detectSessionSource(detail *interfaces.SessionDetail) string {
	if text := readSessionEnvString(detail, "source", "execution_source", "capability_type"); text != "" {
		return text
	}
	return defaultSessionSource
}

func readSessionEnvString(detail *interfaces.SessionDetail, keys ...string) string {
	if detail == nil || len(detail.EnvVars) == 0 {
		return ""
	}
	for _, key := range keys {
		if value, ok := detail.EnvVars[key]; ok {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" {
				return text
			}
		}
	}
	return ""
}

func summarizeSessionError(detail *interfaces.SessionDetail) string {
	if detail == nil {
		return ""
	}
	if text := strings.TrimSpace(detail.DependencyInstallError); text != "" {
		return text
	}
	if detail.Status == interfaces.SessionStatusFailed {
		return "session failed"
	}
	return ""
}

func matchSessionFilters(summary *SandboxSessionSummary, req *SandboxSessionListReq) bool {
	if summary == nil || req == nil {
		return true
	}
	if req.Source != "" && summary.Source != req.Source {
		return false
	}
	if req.Runtime != "" && summary.RuntimeType != req.Runtime && summary.LanguageRuntime != req.Runtime {
		return false
	}
	if req.AbnormalOnly && !isAbnormalSummary(summary) {
		return false
	}
	return true
}

func isAbnormalSession(detail *interfaces.SessionDetail) bool {
	if detail == nil {
		return false
	}
	return detail.Status == interfaces.SessionStatusFailed ||
		detail.DependencyInstallStatus == "failed" ||
		strings.TrimSpace(detail.DependencyInstallError) != ""
}

func isAbnormalSummary(summary *SandboxSessionSummary) bool {
	return summary.Status == interfaces.SessionStatusFailed ||
		summary.DependencyInstallStatus == "failed" ||
		strings.TrimSpace(summary.RecentErrorSummary) != ""
}
