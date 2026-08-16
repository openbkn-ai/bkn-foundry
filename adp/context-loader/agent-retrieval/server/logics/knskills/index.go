// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knskills provides skill browsing, reading, and execution after
// find_skills. The latter returns only skill_id, name, and description.
//
// This layer formats results for models: text detection, size truncation, and
// empty-result messages. Driven adapters perform the metadata and object-store calls.
package knskills

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const (
	// maxDocChars is the character limit for SKILL.md and skill files returned to a model.
	// Mark truncated content explicitly instead of silently dropping its tail.
	maxDocChars = 40000
	// maxStreamChars is the character limit for each execution stdout and stderr stream.
	maxStreamChars = 8000
)

// ErrSkillIDRequired identifies a missing skill_id argument.
var ErrSkillIDRequired = errors.New("skill_id is required")

// ErrRelPathRequired identifies a missing rel_path argument.
var ErrRelPathRequired = errors.New("rel_path is required")

// ErrEntryShellRequired identifies a missing entry_shell argument.
var ErrEntryShellRequired = errors.New("entry_shell is required")

type localizedInputError struct {
	message string
	cause   error
}

func (e localizedInputError) Error() string { return e.message }

func (e localizedInputError) Unwrap() error { return e.cause }

// SkillIDRequiredError returns a localized missing skill_id error.
func SkillIDRequiredError(ctx context.Context) error {
	return localizedInputError{message: infraErr.LocalizedDetail(ctx, "SkillIDRequired"), cause: ErrSkillIDRequired}
}

// RelPathRequiredError returns a localized missing rel_path error.
func RelPathRequiredError(ctx context.Context) error {
	return localizedInputError{message: infraErr.LocalizedDetail(ctx, "SkillRelativePathRequired"), cause: ErrRelPathRequired}
}

// EntryShellRequiredError returns a localized missing entry_shell error.
func EntryShellRequiredError(ctx context.Context) error {
	return localizedInputError{message: infraErr.LocalizedDetail(ctx, "SkillEntryShellRequired"), cause: ErrEntryShellRequired}
}

// ListSkillsReq is the input for list_skills.
type ListSkillsReq struct {
	Name     string `json:"name"`      // 可选，按名称模糊过滤
	Category string `json:"category"`  // 可选，按分类过滤
	Page     int    `json:"page"`      // 可选，页码，从 1 开始
	PageSize int    `json:"page_size"` // 可选，每页大小
}

// SkillEntry is a skill entry returned by list_skills.
type SkillEntry struct {
	SkillID     string `json:"skill_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Category    string `json:"category,omitempty"`
}

// ListSkillsResp is the list_skills response.
type ListSkillsResp struct {
	Entries    []SkillEntry `json:"entries"`
	TotalCount int          `json:"total_count"`
	Page       int          `json:"page,omitempty"`
	PageSize   int          `json:"page_size,omitempty"`
	Message    string       `json:"message,omitempty"`
}

// SkillFileEntry describes a file in a skill package for progressive reading.
type SkillFileEntry struct {
	RelPath  string `json:"rel_path"`
	FileType string `json:"file_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

// GetSkillContentResp is the get_skill_content response: SKILL.md and its file list.
type GetSkillContentResp struct {
	SkillID   string           `json:"skill_id"`
	Status    string           `json:"status,omitempty"`
	Content   string           `json:"content"`
	Truncated bool             `json:"truncated,omitempty"`
	Files     []SkillFileEntry `json:"files"`
	Message   string           `json:"message,omitempty"`
}

// ReadSkillFileReq is the input for read_skill_file.
type ReadSkillFileReq struct {
	SkillID string `json:"skill_id"`
	RelPath string `json:"rel_path"`
}

// ReadSkillFileResp is the read_skill_file response.
// Binary files return metadata and a message instead of content.
type ReadSkillFileResp struct {
	SkillID   string `json:"skill_id"`
	RelPath   string `json:"rel_path"`
	MimeType  string `json:"mime_type,omitempty"`
	FileType  string `json:"file_type,omitempty"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Message   string `json:"message,omitempty"`
}

// ExecuteSkillReq is the input for execute_skill.
type ExecuteSkillReq struct {
	SkillID    string `json:"skill_id"`
	EntryShell string `json:"entry_shell"`
	Timeout    int    `json:"timeout"` // 秒，可选
}

// ExecuteSkillResp is the execute_skill response.
type ExecuteSkillResp struct {
	SkillID       string `json:"skill_id"`
	ExitCode      int    `json:"exit_code"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
	ExecutionTime int64  `json:"execution_time,omitempty"`
	WorkDir       string `json:"work_dir,omitempty"`
	Command       string `json:"command,omitempty"`
	Mocked        bool   `json:"mocked,omitempty"`
}

// KnSkillsService supports skill browsing, reading, and execution.
type KnSkillsService interface {
	ListSkills(ctx context.Context, req *ListSkillsReq) (*ListSkillsResp, error)
	GetSkillContent(ctx context.Context, skillID string) (*GetSkillContentResp, error)
	ReadSkillFile(ctx context.Context, req *ReadSkillFileReq) (*ReadSkillFileResp, error)
	ExecuteSkill(ctx context.Context, req *ExecuteSkillReq) (*ExecuteSkillResp, error)
}

type knSkillsService struct {
	operator interfaces.DrivenOperatorIntegration
}

var (
	once     sync.Once
	instance KnSkillsService
)

// NewKnSkillsService creates the KnSkillsService singleton.
func NewKnSkillsService() KnSkillsService {
	once.Do(func() {
		instance = &knSkillsService{operator: drivenadapters.NewOperatorIntegrationClient()}
	})
	return instance
}

// NewKnSkillsServiceWith creates a service with injected dependencies for tests.
func NewKnSkillsServiceWith(operator interfaces.DrivenOperatorIntegration) KnSkillsService {
	return &knSkillsService{operator: operator}
}

// ListSkills lists published skills. Unlike find_skills, it does not require a
// knowledge-network context.
func (s *knSkillsService) ListSkills(ctx context.Context, req *ListSkillsReq) (*ListSkillsResp, error) {
	if req == nil {
		req = &ListSkillsReq{}
	}
	resp, err := s.operator.ListSkills(ctx, &interfaces.ListSkillsRequest{
		Name:     strings.TrimSpace(req.Name),
		Category: strings.TrimSpace(req.Category),
		Page:     req.Page,
		PageSize: req.PageSize,
	})
	if err != nil {
		return nil, err
	}

	out := &ListSkillsResp{
		Entries:    make([]SkillEntry, 0, len(resp.Entries)),
		TotalCount: resp.TotalCount,
		Page:       resp.Page,
		PageSize:   resp.PageSize,
	}
	for _, item := range resp.Entries {
		out.Entries = append(out.Entries, SkillEntry{
			SkillID:     item.SkillID,
			Name:        item.Name,
			Description: item.Description,
			Version:     item.Version,
			Category:    item.Category,
		})
	}
	if len(out.Entries) == 0 {
		out.Message = infraErr.LocalizedDetail(ctx, "NoPublishedSkillsMatched")
	}
	return out, nil
}

// GetSkillContent returns the skill document and its file list for progressive reading.
func (s *knSkillsService) GetSkillContent(ctx context.Context, skillID string) (*GetSkillContentResp, error) {
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return nil, SkillIDRequiredError(ctx)
	}
	resp, err := s.operator.GetSkillContent(ctx, skillID)
	if err != nil {
		return nil, err
	}

	content, truncated := truncateRunes(string(resp.Content), maxDocChars)
	out := &GetSkillContentResp{
		SkillID:   resp.SkillID,
		Status:    resp.Status,
		Content:   content,
		Truncated: truncated,
		Files:     make([]SkillFileEntry, 0, len(resp.Files)),
	}
	for _, f := range resp.Files {
		out.Files = append(out.Files, SkillFileEntry{
			RelPath:  f.RelPath,
			FileType: f.FileType,
			Size:     f.Size,
			MimeType: f.MimeType,
		})
	}
	if truncated {
		out.Message = infraErr.LocalizedDetail(ctx, "SkillContentTruncated")
	}
	return out, nil
}

// ReadSkillFile reads one skill package file. Binary files return metadata only.
func (s *knSkillsService) ReadSkillFile(ctx context.Context, req *ReadSkillFileReq) (*ReadSkillFileResp, error) {
	if req == nil {
		return nil, SkillIDRequiredError(ctx)
	}
	skillID := strings.TrimSpace(req.SkillID)
	if skillID == "" {
		return nil, SkillIDRequiredError(ctx)
	}
	relPath := strings.TrimSpace(req.RelPath)
	if relPath == "" {
		return nil, RelPathRequiredError(ctx)
	}

	resp, err := s.operator.ReadSkillFile(ctx, &interfaces.ReadSkillFileRequest{SkillID: skillID, RelPath: relPath})
	if err != nil {
		return nil, err
	}

	out := &ReadSkillFileResp{
		SkillID:  resp.SkillID,
		RelPath:  resp.RelPath,
		MimeType: resp.MimeType,
		FileType: resp.FileType,
	}
	if !isTextual(resp.MimeType, resp.Content) {
		out.Message = infraErr.LocalizedDetail(ctx, "SkillFileNotTextual")
		return out, nil
	}
	out.Content, out.Truncated = truncateRunes(string(resp.Content), maxDocChars)
	if out.Truncated {
		out.Message = infraErr.LocalizedDetail(ctx, "SkillFileTruncated")
	}
	return out, nil
}

// ExecuteSkill runs a skill entry command in the sandbox.
// entry_shell comes from SKILL.md; Execution Factory enforces account authorization.
func (s *knSkillsService) ExecuteSkill(ctx context.Context, req *ExecuteSkillReq) (*ExecuteSkillResp, error) {
	if req == nil {
		return nil, SkillIDRequiredError(ctx)
	}
	skillID := strings.TrimSpace(req.SkillID)
	if skillID == "" {
		return nil, SkillIDRequiredError(ctx)
	}
	entryShell := strings.TrimSpace(req.EntryShell)
	if entryShell == "" {
		return nil, EntryShellRequiredError(ctx)
	}

	resp, err := s.operator.ExecuteSkill(ctx, &interfaces.ExecuteSkillRequest{
		SkillID:    skillID,
		EntryShell: entryShell,
		Timeout:    req.Timeout,
	})
	if err != nil {
		return nil, err
	}

	stdout, stdoutTruncated := truncateRunes(resp.Stdout, maxStreamChars)
	stderr, stderrTruncated := truncateRunes(resp.Stderr, maxStreamChars)
	return &ExecuteSkillResp{
		SkillID:       resp.SkillID,
		ExitCode:      resp.ExitCode,
		Stdout:        stdout,
		Stderr:        stderr,
		Truncated:     stdoutTruncated || stderrTruncated,
		ExecutionTime: resp.ExecutionTime,
		WorkDir:       resp.WorkDir,
		Command:       resp.Command,
		Mocked:        resp.Mocked,
	}, nil
}

// truncateRunes truncates by characters rather than bytes to avoid splitting UTF-8 sequences.
func truncateRunes(text string, limit int) (string, bool) {
	if utf8.RuneCountInString(text) <= limit {
		return text, false
	}
	count := 0
	for idx := range text {
		if count == limit {
			return text[:idx], true
		}
		count++
	}
	return text, false
}

// isTextual determines whether a file body can be returned directly to a model.
// MIME type is weak evidence, so UTF-8 validity is the final check.
func isTextual(mimeType string, content []byte) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"),
		strings.HasPrefix(mimeType, "audio/"),
		strings.HasPrefix(mimeType, "video/"),
		strings.HasPrefix(mimeType, "application/zip"),
		strings.HasPrefix(mimeType, "application/x-tar"),
		strings.HasPrefix(mimeType, "application/gzip"),
		strings.HasPrefix(mimeType, "application/pdf"):
		return false
	}
	if !utf8.Valid(content) {
		return false
	}
	// NUL bytes in otherwise valid UTF-8 strongly indicate binary content.
	return !strings.ContainsRune(string(content), '\x00')
}

// ExecuteEnabledEnv controls whether execute_skill is registered for MCP and REST.
const ExecuteEnabledEnv = "EXECUTE_SKILL_ENABLED"

// legacyExecuteEnabledEnv is the legacy name from the MCP-only configuration.
// Continue supporting it so upgrades do not disable an already enabled capability.
const legacyExecuteEnabledEnv = "MCP_EXECUTE_SKILL_ENABLED"

// ExecuteEnabled determines whether the deployment provides skill execution.
// It defaults to disabled and gates both MCP registration and REST routes.
func ExecuteEnabled() bool {
	for _, key := range []string{ExecuteEnabledEnv, legacyExecuteEnabledEnv} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		enabled, err := strconv.ParseBool(value)
		return err == nil && enabled
	}
	return false
}
