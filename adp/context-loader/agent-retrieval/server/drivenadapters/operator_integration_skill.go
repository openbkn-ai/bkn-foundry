// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

const (
	// https://{host}:{port}/api/agent-operator-integration/internal-v1/skills/market
	listSkillsURI = "/internal-v1/skills/market"
	// https://{host}:{port}/api/agent-operator-integration/internal-v1/skills/:skill_id/content
	getSkillContentURI = "/internal-v1/skills/%s/content"
	// https://{host}:{port}/api/agent-operator-integration/internal-v1/skills/:skill_id/files/read
	readSkillFileURI = "/internal-v1/skills/%s/files/read"
	// https://{host}:{port}/api/agent-operator-integration/internal-v1/skills/:skill_id/execute
	executeSkillURI = "/internal-v1/skills/%s/execute"

	defaultSkillPageSize = 20
	// maxSkillAssetBytes limits one skill asset fetched from object storage. Return
	// an error instead of truncating because truncation can split a multibyte character.
	maxSkillAssetBytes = 5 << 20
)

// ListSkills lists published skills from Execution Factory's skill marketplace.
func (o *operatorIntegrationClient) ListSkills(ctx context.Context, req *interfaces.ListSkillsRequest) (*interfaces.ListSkillsResponse, error) {
	if req == nil {
		req = &interfaces.ListSkillsRequest{}
	}
	page, pageSize := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defaultSkillPageSize
	}

	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(pageSize))
	if req.Name != "" {
		query.Set("name", req.Name)
	}
	if req.Category != "" {
		query.Set("category", req.Category)
	}

	fullURL := o.baseURL + listSkillsURI
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ListSkills] URL: %s?%s", fullURL, query.Encode())

	header := o.skillHeader(ctx, "operator.skill.list")
	code, respBody, err := o.httpClient.Get(ctx, fullURL, query, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ListSkills] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "SkillListRequestFailed", err)
	}

	var raw struct {
		Total    int `json:"total"`
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
		Data     []struct {
			SkillID     string `json:"skill_id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Version     string `json:"version"`
			Category    string `json:"category"`
			Status      string `json:"status"`
		} `json:"data"`
	}
	if err = json.Unmarshal(utils.ObjectToByte(respBody), &raw); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ListSkills] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "SkillListResponseInvalid"))
	}

	resp := &interfaces.ListSkillsResponse{
		Entries:    make([]interfaces.SkillBrief, 0, len(raw.Data)),
		TotalCount: raw.Total,
		Page:       raw.Page,
		PageSize:   raw.PageSize,
	}
	for _, item := range raw.Data {
		resp.Entries = append(resp.Entries, interfaces.SkillBrief{
			SkillID:     item.SkillID,
			Name:        item.Name,
			Description: item.Description,
			Version:     item.Version,
			Category:    item.Category,
			Status:      item.Status,
		})
	}
	return resp, nil
}

// GetSkillContent returns the skill document body and its file list.
// Execution Factory returns a presigned URL, which this client follows to get the body.
func (o *operatorIntegrationClient) GetSkillContent(ctx context.Context, skillID string) (*interfaces.GetSkillContentResponse, error) {
	fullURL := o.baseURL + fmt.Sprintf(getSkillContentURI, url.PathEscape(skillID))
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#GetSkillContent] URL: %s", fullURL)

	header := o.skillHeader(ctx, "operator.skill.content")
	code, respBody, err := o.httpClient.Get(ctx, fullURL, nil, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetSkillContent] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "SkillContentRequestFailed", err)
	}

	var raw struct {
		SkillID string                        `json:"skill_id"`
		URL     string                        `json:"url"`
		Status  string                        `json:"status"`
		Files   []interfaces.SkillFileSummary `json:"files"`
	}
	if err = json.Unmarshal(utils.ObjectToByte(respBody), &raw); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetSkillContent] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "SkillContentResponseInvalid"))
	}

	content, err := o.fetchSkillAsset(ctx, raw.URL)
	if err != nil {
		return nil, err
	}
	return &interfaces.GetSkillContentResponse{
		SkillID: firstNonEmptyStr(raw.SkillID, skillID),
		Content: content,
		Status:  raw.Status,
		Files:   raw.Files,
	}, nil
}

// ReadSkillFile reads one file from a skill package using the same two-hop flow.
func (o *operatorIntegrationClient) ReadSkillFile(ctx context.Context, req *interfaces.ReadSkillFileRequest) (*interfaces.ReadSkillFileResponse, error) {
	fullURL := o.baseURL + fmt.Sprintf(readSkillFileURI, url.PathEscape(req.SkillID))
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ReadSkillFile] URL: %s, RelPath: %s", fullURL, req.RelPath)

	header := o.skillHeader(ctx, "operator.skill.file_read")
	// Execution Factory requires the rel_path field; sending path returns 400.
	code, respBody, err := o.httpClient.Post(ctx, fullURL, header, map[string]string{"rel_path": req.RelPath})
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ReadSkillFile] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "SkillFileReadRequestFailed", err)
	}

	var raw struct {
		SkillID  string `json:"skill_id"`
		RelPath  string `json:"rel_path"`
		URL      string `json:"url"`
		MimeType string `json:"mime_type"`
		FileType string `json:"file_type"`
	}
	if err = json.Unmarshal(utils.ObjectToByte(respBody), &raw); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ReadSkillFile] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "SkillFileReadResponseInvalid"))
	}

	content, err := o.fetchSkillAsset(ctx, raw.URL)
	if err != nil {
		return nil, err
	}
	return &interfaces.ReadSkillFileResponse{
		SkillID:  firstNonEmptyStr(raw.SkillID, req.SkillID),
		RelPath:  firstNonEmptyStr(raw.RelPath, req.RelPath),
		MimeType: raw.MimeType,
		FileType: raw.FileType,
		Content:  content,
	}, nil
}

// ExecuteSkill runs a skill entry command in the sandbox. Execution Factory
// enforces account authorization (execute / public_access).
func (o *operatorIntegrationClient) ExecuteSkill(ctx context.Context, req *interfaces.ExecuteSkillRequest) (*interfaces.ExecuteSkillResponse, error) {
	fullURL := o.baseURL + fmt.Sprintf(executeSkillURI, url.PathEscape(req.SkillID))
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ExecuteSkill] URL: %s", fullURL)

	header := o.skillHeader(ctx, "operator.skill.execute")
	body := map[string]any{"entry_shell": req.EntryShell}
	if req.Timeout > 0 {
		body["timeout"] = req.Timeout
	}
	code, respBody, err := o.httpClient.Post(ctx, fullURL, header, body)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ExecuteSkill] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "SkillExecutionRequestFailed", err)
	}

	resp := &interfaces.ExecuteSkillResponse{}
	if err = json.Unmarshal(utils.ObjectToByte(respBody), resp); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ExecuteSkill] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "SkillExecutionResponseInvalid"))
	}
	if resp.SkillID == "" {
		resp.SkillID = req.SkillID
	}
	return resp, nil
}

// fetchSkillAsset retrieves a skill file body from object storage.
// It is the second hop for a published skill because the metadata API returns only a presigned URL.
func (o *operatorIntegrationClient) fetchSkillAsset(ctx context.Context, assetURL string) ([]byte, error) {
	if assetURL == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusNotFound,
			infraErr.LocalizedDetail(ctx, "SkillAssetURLMissing"))
	}
	// The presigned URL carries its own signature. Do not send account or trace
	// headers because some object stores include additional headers in signature validation.
	code, body, err := o.httpClient.GetNoUnmarshal(ctx, assetURL, nil, nil)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#fetchSkillAsset] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "SkillAssetDownloadFailed"))
	}
	if code < http.StatusOK || code >= http.StatusMultipleChoices {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "SkillAssetUnexpectedStatus"))
	}
	if len(body) > maxSkillAssetBytes {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusRequestEntityTooLarge,
			infraErr.LocalizedDetail(ctx, "SkillAssetTooLarge"))
	}
	return body, nil
}

// skillHeader builds outbound account, trace, and business-domain headers.
// The skill marketplace requires a business domain, so use the default if absent.
func (o *operatorIntegrationClient) skillHeader(ctx context.Context, operationName string) map[string]string {
	header := common.GetHeaderForChildOperation(ctx, operationName, 1)
	if bd := header[string(interfaces.HeaderXBusinessDomain)]; bd == "" {
		header[string(interfaces.HeaderXBusinessDomain)] = interfaces.DefaultBusinessDomainID
	}
	return header
}

// skillUpstreamError classifies an Execution Factory failure by status code.
//
// A downstream 4xx is a caller error (for example, an invalid package path,
// a missing skill, or insufficient permission), so preserve its status code.
// Transport errors and 5xx responses are upstream failures and become 502.
func skillUpstreamError(ctx context.Context, code int, detailKey string, _ error) error {
	if code >= http.StatusBadRequest && code < http.StatusInternalServerError {
		return infraErr.DefaultHTTPError(ctx, code, infraErr.LocalizedDetail(ctx, detailKey))
	}
	return infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, infraErr.LocalizedDetail(ctx, detailKey))
}

func firstNonEmptyStr(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
