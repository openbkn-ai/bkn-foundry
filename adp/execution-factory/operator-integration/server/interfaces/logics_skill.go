package interfaces

import (
	"context"
	"encoding/json"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

//go:generate mockgen -source=logics_skill.go -destination=../mocks/logics_skill.go -package=mocks

// RegisterSkillReq Register Skill request.
type RegisterSkillReq struct {
	BusinessDomainID string          `header:"x-business-domain" validate:"required"`
	UserID           string          `header:"user_id" validate:"required"`
	FileType         string          `form:"file_type" validate:"required,oneof=zip content"`
	File             json.RawMessage `form:"file" validate:"required"`
	Category         BizCategory     `form:"category" default:"other_category" validate:"required"`
	Source           string          `form:"source" default:"custom" validate:"oneof=custom internal"`
	ExtendInfo       json.RawMessage `form:"extend_info"`
}

// RegisterSkillResp Register Skill response.
type RegisterSkillResp struct {
	SkillID     string    `json:"skill_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     string    `json:"version"`
	Status      BizStatus `json:"status"`
	Files       []string  `json:"files"`
}

// DeleteSkillReq Delete Skill request.
type DeleteSkillReq struct {
	BusinessDomainID string `header:"x-business-domain" validate:"required"`
	UserID           string `header:"user_id" validate:"required"`
	SkillID          string `uri:"skill_id" validate:"required"`
}

// DownloadSkillReq Download Skill Request.
type DownloadSkillReq struct {
	BusinessDomainID string `header:"x-business-domain" validate:"required"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
}

// DownloadSkillResp Download Skill response.
type DownloadSkillResp struct {
	SkillID  string `json:"skill_id"`
	FileName string `json:"file_name"`
	Content  []byte `json:"content"`
}

// QuerySkillListReq Skill list query.
type QuerySkillListReq struct {
	BusinessDomainID string      `header:"x-business-domain" validate:"required"`
	UserID           string      `header:"user_id"`
	Name             string      `form:"name"`
	Status           BizStatus   `form:"status" validate:"omitempty,oneof=unpublish published offline editing"`
	Category         BizCategory `form:"category"`
	CreateUser       string      `form:"create_user"`
	CommonPageParams `json:",inline"`
}

// SkillInfo Skill details.
type SkillInfo struct {
	SkillID          string         `json:"skill_id"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	Version          string         `json:"version"`
	Status           BizStatus      `json:"status"`
	Source           string         `json:"source"`
	Dependencies     map[string]any `json:"dependencies,omitempty"`
	ExtendInfo       map[string]any `json:"extend_info,omitempty"`
	CreateUser       string         `json:"create_user"`
	CreateTime       int64          `json:"create_time"`
	UpdateUser       string         `json:"update_user"`
	UpdateTime       int64          `json:"update_time"`
	Category         BizCategory    `json:"category,omitempty"`
	CategoryName     string         `json:"category_name,omitempty"`
	BusinessDomainID string         `json:"business_domain_id"`
	ReleaseUser      string         `json:"release_user,omitempty"`
	ReleaseTime      int64          `json:"release_time,omitempty"`
}

// SkillFileSummary Skill file summary.
type SkillFileSummary struct {
	RelPath  string `json:"rel_path"`
	FileType string `json:"file_type"`
	Size     int64  `json:"size"`
	MimeType string `json:"mime_type"`
}

// QuerySkillListResp Skill list response.
type QuerySkillListResp struct {
	CommonPageResult `json:",inline"`
	Data             []*SkillInfo `json:"data"`
}

// QuerySkillMarketListReq Skill market list query.
type QuerySkillMarketListReq struct {
	BusinessDomainID string      `header:"x-business-domain" validate:"required"`
	UserID           string      `header:"user_id"`
	Name             string      `form:"name"`
	Category         BizCategory `form:"category"`
	CreateUser       string      `form:"create_user"`
	CommonPageParams `json:",inline"`
}

// QuerySkillMarketListResp Skill market list response.
type QuerySkillMarketListResp struct {
	CommonPageResult `json:",inline"`
	Data             []*SkillInfo `json:"data"`
}

// GetSkillDetailReq Skill details query.
type GetSkillDetailReq struct {
	BusinessDomainID string `header:"x-business-domain"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
}

// GetSkillMarketDetailReq Skill market details query.
type GetSkillMarketDetailReq struct {
	BusinessDomainID string `header:"x-business-domain"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
}

// GetSkillContentReq Skill content query.
type GetSkillContentReq struct {
	BusinessDomainID string `header:"x-business-domain"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
}

// GetSkillContentResp Skill content response.
type GetSkillContentResp struct {
	SkillID string              `json:"skill_id"`
	URL     string              `json:"url"`
	Files   []*SkillFileSummary `json:"files"`
	Status  BizStatus           `json:"status"`
}

// ReadSkillFileReq Read Skill file request.
type ReadSkillFileReq struct {
	BusinessDomainID string `header:"x-business-domain"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
	RelPath          string `json:"rel_path" validate:"required"`
}

// ReadSkillFileResp Read Skill file response.
type ReadSkillFileResp struct {
	SkillID  string `json:"skill_id"`
	RelPath  string `json:"rel_path"`
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
	FileType string `json:"file_type"`
}

// GetSkillReleaseHistoryReq Query Skill release history requests.
type GetSkillReleaseHistoryReq struct {
	BusinessDomainID string `header:"x-business-domain" validate:"required"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
}

// SkillReleaseHistoryInfo Skill release history summary.
type SkillReleaseHistoryInfo struct {
	SkillID     string      `json:"skill_id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Version     string      `json:"version"`
	Status      BizStatus   `json:"status"`
	Category    BizCategory `json:"category,omitempty"`
	Source      string      `json:"source"`
	ReleaseDesc string      `json:"release_desc"`
	ReleaseUser string      `json:"release_user"`
	ReleaseTime int64       `json:"release_time"`
	CreateUser  string      `json:"create_user"`
	CreateTime  int64       `json:"create_time"`
	UpdateUser  string      `json:"update_user"`
	UpdateTime  int64       `json:"update_time"`
}

// UpdateSkillStatusReq Update Skill status request.
type UpdateSkillStatusReq struct {
	BusinessDomainID string    `header:"x-business-domain" validate:"required"`
	UserID           string    `header:"user_id"`
	SkillID          string    `uri:"skill_id" validate:"required"`
	Status           BizStatus `json:"status" validate:"required,oneof=published offline"`
}

// UpdateSkillStatusResp Update Skill status response.
type UpdateSkillStatusResp struct {
	SkillID string    `json:"skill_id"`
	Status  BizStatus `json:"status"`
}

// UpdateSkillMetadataReq Update Skill metadata request.
type UpdateSkillMetadataReq struct {
	BusinessDomainID string          `header:"x-business-domain" validate:"required"`
	UserID           string          `header:"user_id"`
	SkillID          string          `uri:"skill_id" validate:"required"`
	Name             string          `json:"name" validate:"required"`
	Description      string          `json:"description" validate:"required"`
	Category         BizCategory     `json:"category" validate:"required"`
	Source           string          `json:"source" validate:"omitempty,oneof=custom internal"`
	ExtendInfo       json.RawMessage `json:"extend_info"`
}

// UpdateSkillMetadataResp Update Skill metadata response.
type UpdateSkillMetadataResp struct {
	SkillID string    `json:"skill_id"`
	Version string    `json:"version"`
	Status  BizStatus `json:"status"`
}

// UpdateSkillPackageReq Update Skill package request.
type UpdateSkillPackageReq struct {
	BusinessDomainID string          `header:"x-business-domain" validate:"required"`
	UserID           string          `header:"user_id"`
	SkillID          string          `uri:"skill_id" validate:"required"`
	FileType         string          `form:"file_type" validate:"required,oneof=zip content"`
	File             json.RawMessage `form:"file" validate:"required"`
}

// UpdateSkillPackageResp Update Skill package response.
type UpdateSkillPackageResp struct {
	SkillID string    `json:"skill_id"`
	Version string    `json:"version"`
	Status  BizStatus `json:"status"`
}

// RepublishSkillHistoryReq returns historical versions to draft requests.
type RepublishSkillHistoryReq struct {
	BusinessDomainID string `header:"x-business-domain" validate:"required"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
	Version          string `json:"version" validate:"required"`
}

// RepublishSkillHistoryResp reverts historical versions to draft responses.
type RepublishSkillHistoryResp struct {
	SkillID string    `json:"skill_id"`
	Version string    `json:"version"`
	Status  BizStatus `json:"status"`
}

// PublishSkillHistoryReq directly publishes historical version requests.
type PublishSkillHistoryReq struct {
	BusinessDomainID string `header:"x-business-domain" validate:"required"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
	Version          string `json:"version" validate:"required"`
}

// PublishSkillHistoryResp directly publishes historical version responses.
type PublishSkillHistoryResp struct {
	SkillID string    `json:"skill_id"`
	Version string    `json:"version"`
	Status  BizStatus `json:"status"`
}

// ExecuteSkillReq executes Skill request.
type ExecuteSkillReq struct {
	BusinessDomainID string `header:"x-business-domain"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
	EntryShell       string `json:"entry_shell" validate:"required"`
	Timeout          int    `json:"timeout,omitempty"`
}

// ExecuteSkillResp Execute Skill response.
type ExecuteSkillResp struct {
	SkillID       string `json:"skill_id"`
	SessionID     string `json:"session_id"`
	WorkDir       string `json:"work_dir"`
	FileName      string `json:"file_name"`
	UploadedPath  string `json:"uploaded_path"`
	Command       string `json:"command"`
	ExitCode      int    `json:"exit_code"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	ExecutionTime int64  `json:"execution_time"`
	Mocked        bool   `json:"mocked"`
}

type SkillIndexBuildExecuteType string

func (e SkillIndexBuildExecuteType) String() string {
	return string(e)
}

const (
	SkillIndexBuildExecuteTypeFull        SkillIndexBuildExecuteType = "full"
	SkillIndexBuildExecuteTypeIncremental SkillIndexBuildExecuteType = "incremental"
)

type SkillIndexBuildStatus string

func (s SkillIndexBuildStatus) String() string {
	return string(s)
}

const (
	SkillIndexBuildStatusPending   SkillIndexBuildStatus = "pending"
	SkillIndexBuildStatusRunning   SkillIndexBuildStatus = "running"
	SkillIndexBuildStatusCompleted SkillIndexBuildStatus = "completed"
	SkillIndexBuildStatusFailed    SkillIndexBuildStatus = "failed"
	SkillIndexBuildStatusCanceled  SkillIndexBuildStatus = "canceled"
)

type CreateSkillIndexBuildTaskReq struct {
	BusinessDomainID string                     `header:"x-business-domain" validate:"required"`
	UserID           string                     `header:"user_id" validate:"required"`
	ExecuteType      SkillIndexBuildExecuteType `json:"execute_type" validate:"required,oneof=full incremental"`
}

type CreateSkillIndexBuildTaskResp struct {
	TaskID      string                `json:"task_id"`
	Status      SkillIndexBuildStatus `json:"status"`
	ExecuteType string                `json:"execute_type"`
}

type GetSkillIndexBuildTaskReq struct {
	BusinessDomainID string `header:"x-business-domain" validate:"required"`
	UserID           string `header:"user_id"`
	TaskID           string `uri:"task_id" validate:"required"`
}

type CancelSkillIndexBuildTaskReq struct {
	BusinessDomainID string `header:"x-business-domain" validate:"required"`
	UserID           string `header:"user_id" validate:"required"`
	TaskID           string `uri:"task_id" validate:"required"`
}

type CancelSkillIndexBuildTaskResp struct {
	TaskID     string `json:"task_id"`
	Action     string `json:"action"`
	QueueState string `json:"queue_state,omitempty"`
}

type RetrySkillIndexBuildTaskReq struct {
	BusinessDomainID string `header:"x-business-domain" validate:"required"`
	UserID           string `header:"user_id" validate:"required"`
	TaskID           string `uri:"task_id" validate:"required"`
}

type RetrySkillIndexBuildTaskResp struct {
	SourceTaskID string                `json:"source_task_id"`
	TaskID       string                `json:"task_id"`
	Status       SkillIndexBuildStatus `json:"status"`
	ExecuteType  string                `json:"execute_type"`
}

type QuerySkillIndexBuildTaskListReq struct {
	BusinessDomainID string                `header:"x-business-domain" validate:"required"`
	UserID           string                `header:"user_id"`
	Status           SkillIndexBuildStatus `form:"status" validate:"omitempty,oneof=pending running completed failed"`
	ExecuteType      string                `form:"execute_type" validate:"omitempty,oneof=full incremental"`
	CreateUser       string                `form:"create_user"`
	CommonPageParams `json:",inline"`
}

type QuerySkillIndexBuildTaskListResp struct {
	CommonPageResult `json:",inline"`
	Data             []*SkillIndexBuildTaskResp `json:"data"`
}

type SkillIndexBuildTaskResp struct {
	TaskID           string                `json:"task_id"`
	Status           SkillIndexBuildStatus `json:"status"`
	ExecuteType      string                `json:"execute_type"`
	QueueState       string                `json:"queue_state"`
	TotalCount       int64                 `json:"total_count"`
	SuccessCount     int64                 `json:"success_count"`
	DeleteCount      int64                 `json:"delete_count"`
	FailedCount      int64                 `json:"failed_count"`
	RetryCount       int64                 `json:"retry_count"`
	MaxRetry         int64                 `json:"max_retry"`
	CursorUpdateTime int64                 `json:"cursor_update_time"`
	CursorSkillID    string                `json:"cursor_skill_id"`
	ErrorMsg         string                `json:"error_msg"`
	CreateUser       string                `json:"create_user"`
	CreateTime       int64                 `json:"create_time"`
	UpdateTime       int64                 `json:"update_time"`
	LastFinishedTime int64                 `json:"last_finished_time"`
}

// SkillRegistry Skill management interface.
type SkillRegistry interface {
	RegisterSkill(ctx context.Context, req *RegisterSkillReq) (*RegisterSkillResp, error)
	UpdateSkillMetadata(ctx context.Context, req *UpdateSkillMetadataReq) (*UpdateSkillMetadataResp, error)
	UpdateSkillPackage(ctx context.Context, req *UpdateSkillPackageReq) (*UpdateSkillPackageResp, error)
	RepublishSkillHistory(ctx context.Context, req *RepublishSkillHistoryReq) (*RepublishSkillHistoryResp, error)
	PublishSkillHistory(ctx context.Context, req *PublishSkillHistoryReq) (*PublishSkillHistoryResp, error)
	DeleteSkill(ctx context.Context, req *DeleteSkillReq) error
	DownloadSkill(ctx context.Context, req *DownloadSkillReq) (*DownloadSkillResp, error)
	ExecuteSkill(ctx context.Context, req *ExecuteSkillReq) (*ExecuteSkillResp, error)
	QuerySkillList(ctx context.Context, req *QuerySkillListReq) (*QuerySkillListResp, error)
	GetSkillDetail(ctx context.Context, req *GetSkillDetailReq) (*SkillInfo, error)
	// GetSkillNamesByIDs batch names based on skill IDs (fault tolerance: non-existent IDs are ignored)
	GetSkillNamesByIDs(ctx context.Context, ids []string) (*BatchNamesResp, error)
	// Update skill status.
	UpdateSkillStatus(ctx context.Context, req *UpdateSkillStatusReq) (*UpdateSkillStatusResp, error)
}

// SkillMarket Skill market interface.
type SkillMarket interface {
	QuerySkillMarketList(ctx context.Context, req *QuerySkillMarketListReq) (*QuerySkillMarketListResp, error)
	GetSkillMarketDetail(ctx context.Context, req *GetSkillMarketDetailReq) (*SkillInfo, error)
}

// SkillReader Skill read-only interface.
type SkillReader interface {
	GetSkillContent(ctx context.Context, req *GetSkillContentReq) (*GetSkillContentResp, error)
	ReadSkillFile(ctx context.Context, req *ReadSkillFileReq) (*ReadSkillFileResp, error)
	GetSkillReleaseHistory(ctx context.Context, req *GetSkillReleaseHistoryReq) ([]*SkillReleaseHistoryInfo, error)
}

// ========== Management Read ==========

// SkillManagementReader Skill management read-only interface.
type SkillManagementReader interface {
	// GetManagementContent Gets the management state SKILL.md content (including file list)
	GetManagementContent(ctx context.Context, req *GetManagementContentReq) (*GetManagementContentResp, error)
	// ReadManagementFile reads the contents of the specified file in the management state.
	ReadManagementFile(ctx context.Context, req *ReadManagementFileReq) (*ReadManagementFileResp, error)
	// DownloadManagementSkill Download the complete management skills package.
	DownloadManagementSkill(ctx context.Context, req *DownloadManagementSkillReq) (*DownloadSkillResp, error)
}

// GetManagementContentReq management content query request.
type GetManagementContentReq struct {
	BusinessDomainID string `header:"x-business-domain"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
	ResponseMode     string `form:"response_mode" default:"url"` // url(default) | content.
}

// GetManagementContentResp management content query response.
type GetManagementContentResp struct {
	SkillID     string              `json:"skill_id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Version     string              `json:"version"`
	Status      BizStatus           `json:"status"`
	Source      string              `json:"source"`
	FileType    string              `json:"file_type"`
	URL         string              `json:"url"`
	Content     string              `json:"content,omitempty"`
	Files       []*SkillFileSummary `json:"files"`
}

// ReadManagementFileReq management file read request.
type ReadManagementFileReq struct {
	BusinessDomainID string `header:"x-business-domain"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
	RelPath          string `json:"rel_path" validate:"required"`
	ResponseMode     string `form:"response_mode" default:"url"` // url(default) | content.
}

// ReadManagementFileResp management file read response.
type ReadManagementFileResp struct {
	SkillID  string `json:"skill_id"`
	RelPath  string `json:"rel_path"`
	URL      string `json:"url"`
	Content  string `json:"content,omitempty"`
	MimeType string `json:"mime_type"`
	FileType string `json:"file_type"`
	Size     int64  `json:"size"`
}

// DownloadManagementSkillReq Management skill package download request.
type DownloadManagementSkillReq struct {
	BusinessDomainID string `header:"x-business-domain"`
	UserID           string `header:"user_id"`
	SkillID          string `uri:"skill_id" validate:"required"`
}

type SkillIndexBuildService interface {
	// CreateTask Create task.
	CreateTask(ctx context.Context, req *CreateSkillIndexBuildTaskReq) (*CreateSkillIndexBuildTaskResp, error)
	// GetTask Get the task.
	GetTask(ctx context.Context, req *GetSkillIndexBuildTaskReq) (*SkillIndexBuildTaskResp, error)
	// QueryTaskList query task list.
	QueryTaskList(ctx context.Context, req *QuerySkillIndexBuildTaskListReq) (*QuerySkillIndexBuildTaskListResp, error)
	// CancelTask cancels the task.
	CancelTask(ctx context.Context, req *CancelSkillIndexBuildTaskReq) (*CancelSkillIndexBuildTaskResp, error)
	// RetryTask retry task.
	RetryTask(ctx context.Context, req *RetrySkillIndexBuildTaskReq) (*RetrySkillIndexBuildTaskResp, error)
}

type SkillIndexSyncService interface {
	Init(ctx context.Context) error
	EnsureInitialized(ctx context.Context) error
	UpsertSkill(ctx context.Context, skill *model.SkillRepositoryDB) error
	UpdateSkill(ctx context.Context, skill *model.SkillRepositoryDB) error
	DeleteSkill(ctx context.Context, skillID string) error
}
