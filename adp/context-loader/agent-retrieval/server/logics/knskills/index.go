// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knskills provides skill browsing, reading, and execution after
// search_capabilities. The latter returns only capability_id, name, and description for a Skill.
//
// Reading and execution are scoped to the knowledge network that mounted the Skill. Narrowing
// recall is not a control on its own: a skill_id outlives the call that produced it and
// can be had from list_skills, so the check has to sit where the document is read and where the
// entry command runs.
//
// Reading follows the network's authorization (#1550): a caller who may view the network may read
// what it mounted. The read is tried with the caller's own identity first, so a caller who also
// holds a grant on the Skill sees exactly what they saw before; only when the execution factory
// refuses that caller is the same read repeated as the network's managed proxy account, whose
// grant on the Skill was vouched for by the editor who mounted or last published it. Running a
// Skill stays caller-scoped.
//
// This layer formats results for models: text detection, size truncation, and
// empty-result messages. Driven adapters perform the metadata and object-store calls.
package knskills

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/permission"
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
	Name     string `json:"name"`      // Optional, fuzzy filter by name.
	Category string `json:"category"`  // Optional, filter by category.
	Page     int    `json:"page"`      // Optional, page number, starting from 1.
	PageSize int    `json:"page_size"` // Optional, per page size.
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
	// KnID is required: the file is read only when the Skill is mounted on this network.
	KnID    string `json:"kn_id"`
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
	// KnID is required and checked before anything runs. This is the call that changes the world.
	KnID       string `json:"kn_id"`
	SkillID    string `json:"skill_id"`
	EntryShell string `json:"entry_shell"`
	Timeout    int    `json:"timeout"` // seconds, optional.
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
	// ListSkills browses the platform's published Skills and is deliberately not network-scoped:
	// mounting one means first being able to see what exists. Its results are a catalogue, not a
	// permission — everything below refuses a Skill this network has not mounted.
	ListSkills(ctx context.Context, req *ListSkillsReq) (*ListSkillsResp, error)
	GetSkillContent(ctx context.Context, knID, skillID string) (*GetSkillContentResp, error)
	ReadSkillFile(ctx context.Context, req *ReadSkillFileReq) (*ReadSkillFileResp, error)
	ExecuteSkill(ctx context.Context, req *ExecuteSkillReq) (*ExecuteSkillResp, error)
}

type knSkillsService struct {
	operator   interfaces.DrivenOperatorIntegration
	bknBackend interfaces.BknBackendAccess
	knAuthz    interfaces.KnowledgeNetworkAuthorizer
	// logger records every read made as a network's managed proxy. A service
	// built without one (tests) still reads; it just does not log.
	logger interfaces.Logger
}

var (
	once     sync.Once
	instance KnSkillsService
)

// NewKnSkillsService creates the KnSkillsService singleton.
func NewKnSkillsService() KnSkillsService {
	once.Do(func() {
		conf := config.NewConfigLoader()
		instance = &knSkillsService{
			operator:   drivenadapters.NewOperatorIntegrationClient(),
			bknBackend: drivenadapters.NewBknBackendAccess(),
			knAuthz:    permission.NewKnowledgeNetworkAuthorizer(conf),
			logger:     conf.GetLogger(),
		}
	})
	return instance
}

// NewKnSkillsServiceWith creates a service with injected dependencies for tests.
func NewKnSkillsServiceWith(operator interfaces.DrivenOperatorIntegration,
	bknBackend interfaces.BknBackendAccess,
	knAuthz interfaces.KnowledgeNetworkAuthorizer) KnSkillsService {
	return &knSkillsService{operator: operator, bknBackend: bknBackend, knAuthz: knAuthz}
}

// ListSkills lists published skills. Unlike search_capabilities, it does not require a
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

// requireMounted refuses a Skill the knowledge network has not mounted.
//
// Two checks, in this order: the caller may read the network at all, and the network mounted this
// Skill. Both are needed and neither substitutes for the other — the first stops one network's
// mounts from being read through another's id, the second stops a skill_id picked up from
// list_skills or an earlier session from being used here.
//
// Fail-closed throughout: a service wired without the authorizer, an unreadable binding list, and
// an unauthorized caller all refuse. Reading the bindings is the scope, so failing to read them
// cannot degrade into "allow".
//
// It returns the mount, whose id is what the network's proxy grant for this Skill is keyed by.
func (s *knSkillsService) requireMounted(ctx context.Context, knID, skillID string) (*interfaces.CapabilityRef, error) {
	knID = strings.TrimSpace(knID)
	if knID == "" {
		// A 400, not the bare sentinel: a missing argument is the caller's error, and the REST
		// layer turns an unclassified error into a 500 that reads as a platform fault.
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "SkillScopeKnIDRequired"))
	}
	if s.knAuthz == nil || s.bknBackend == nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "SkillAuthorizationUnavailable"))
	}
	if err := s.knAuthz.AuthorizeRead(ctx, knID); err != nil {
		return nil, permission.CapabilityScopeError(ctx, err, permission.CapabilityNetworkViewRequired)
	}

	refs, err := s.bknBackend.ListKNCapabilities(ctx, knID, "", interfaces.CapabilityTypeSkill)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if ref == nil || ref.CapabilityType != interfaces.CapabilityTypeSkill {
			continue
		}
		if strings.TrimSpace(ref.CapabilityID) == skillID {
			return ref, nil
		}
	}
	return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
		infraErr.LocalizedDetail(ctx, "SkillNotMountedOnNetwork"))
}

// proxyReadAccount resolves the managed proxy account a mounted Skill is read as.
//
// The binding is built here from the mount the network reported, never from the request, and
// bkn-backend confirms it is a current published grant source of a synchronized proxy before
// naming the account. A source bkn-backend does not know, which is what a release that predates
// Skill sources answers, means the Skill was never provisioned for network access and is said so.
func (s *knSkillsService) proxyReadAccount(ctx context.Context, knID string, mount *interfaces.CapabilityRef,
	skillID string) (interfaces.SkillAccountReader, interfaces.AccountAuthContext, error) {
	reader, readerOK := s.operator.(interfaces.SkillAccountReader)
	resolver, resolverOK := s.bknBackend.(interfaces.KNProxyResolver)
	if !readerOK || !resolverOK || mount == nil || strings.TrimSpace(mount.ID) == "" {
		return nil, interfaces.AccountAuthContext{}, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "SkillAuthorizationUnavailable"))
	}
	binding := interfaces.KNProxyBinding{
		KNID: strings.TrimSpace(knID), ChildType: interfaces.KNProxyChildTypeCapability,
		ChildID: strings.TrimSpace(mount.ID), TargetType: interfaces.KNProxyTargetTypeSkill,
		TargetID: skillID, Operation: interfaces.KNProxyOperationExecute,
	}
	mapping, err := resolver.ResolveKNProxyBinding(ctx, binding)
	if err != nil {
		return nil, interfaces.AccountAuthContext{}, notProvisionedWhenForbidden(ctx, err)
	}
	if mapping == nil || strings.TrimSpace(mapping.ProxyAccountID) == "" ||
		mapping.ProxyAccountType != string(interfaces.AccessorTypeApp) {
		return nil, interfaces.AccountAuthContext{}, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "SkillAuthorizationUnavailable"))
	}
	return reader, interfaces.AccountAuthContext{
		AccountID: mapping.ProxyAccountID, AccountType: interfaces.AccessorTypeApp,
	}, nil
}

// logProxyRead records the completed read's outcome and identity. The execution factory sees
// only the proxy account, so this is where the caller is tied to the read. A zero HTTP status
// means the failure has no classified status; upstream error details are not logged here.
func (s *knSkillsService) logProxyRead(ctx context.Context, operation, knID string,
	account interfaces.AccountAuthContext, skillID string, err error) {
	if s.logger == nil {
		return
	}
	callerID := ""
	if caller, ok := common.GetAccountAuthContextFromCtx(ctx); ok && caller != nil {
		callerID = caller.AccountID
	}
	result, status := "success", http.StatusOK
	if err != nil {
		result = "failure"
		status, _ = infraErr.HTTPStatus(err)
	}
	s.logger.WithContext(ctx).Infof("[KnSkills] %s read through the knowledge network proxy: caller_id=%s kn_id=%s "+
		"proxy_account_id=%s skill_id=%s result=%s http_status=%d", operation, callerID,
		strings.TrimSpace(knID), account.AccountID, skillID, result, status)
}

// notProvisionedWhenForbidden restates a refused proxy read. A 403 there means the network's proxy
// holds no grant on this Skill — the editor who mounted or last published it could not vouch for
// it — which the caller can act on only if told so. Other statuses keep their own meaning.
func notProvisionedWhenForbidden(ctx context.Context, err error) error {
	if status, ok := infraErr.HTTPStatus(err); ok && status == http.StatusForbidden {
		return infraErr.DefaultHTTPError(ctx, http.StatusForbidden,
			infraErr.LocalizedDetail(ctx, "SkillNotProvisionedForNetwork"))
	}
	return err
}

// callerRefused reports whether the execution factory refused the caller's own read, the one
// outcome that sends the read through the network's proxy.
func callerRefused(err error) bool {
	status, ok := infraErr.HTTPStatus(err)
	return ok && status == http.StatusForbidden
}

// GetSkillContent returns the skill document and its file list for progressive reading.
func (s *knSkillsService) GetSkillContent(ctx context.Context, knID, skillID string) (*GetSkillContentResp, error) {
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return nil, SkillIDRequiredError(ctx)
	}
	mount, err := s.requireMounted(ctx, knID, skillID)
	if err != nil {
		return nil, err
	}
	resp, err := s.operator.GetSkillContent(ctx, skillID)
	if callerRefused(err) {
		reader, account, proxyErr := s.proxyReadAccount(ctx, knID, mount, skillID)
		if proxyErr != nil {
			return nil, proxyErr
		}
		resp, err = reader.GetSkillContentAs(ctx, account, skillID)
		s.logProxyRead(ctx, "skill content", knID, account, skillID, err)
		err = notProvisionedWhenForbidden(ctx, err)
	}
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
	mount, err := s.requireMounted(ctx, req.KnID, skillID)
	if err != nil {
		return nil, err
	}

	fileReq := &interfaces.ReadSkillFileRequest{SkillID: skillID, RelPath: relPath}
	resp, err := s.operator.ReadSkillFile(ctx, fileReq)
	if callerRefused(err) {
		reader, account, proxyErr := s.proxyReadAccount(ctx, req.KnID, mount, skillID)
		if proxyErr != nil {
			return nil, proxyErr
		}
		resp, err = reader.ReadSkillFileAs(ctx, account, fileReq)
		s.logProxyRead(ctx, "skill file", req.KnID, account, skillID, err)
		err = notProvisionedWhenForbidden(ctx, err)
	}
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
	if _, err := s.requireMounted(ctx, req.KnID, skillID); err != nil {
		return nil, err
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
