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
	// maxSkillAssetBytes 单个技能文件从对象存储取回的上限。超出即报错而不是截断：
	// 截断点落在多字节字符中间会产出乱码，交给上层按 mime 决定要不要读更划算。
	maxSkillAssetBytes = 5 << 20
)

// ListSkills 浏览已发布技能（走执行工厂的技能市场列表，只含已发布态）。
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
		return nil, skillUpstreamError(ctx, code, "技能列表接口调用失败", err)
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
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("解析技能列表响应失败: %v", err))
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

// GetSkillContent 取技能主文档正文 + 包内文件清单。
// 执行工厂只回 presigned URL，正文由这里补第二跳取回。
func (o *operatorIntegrationClient) GetSkillContent(ctx context.Context, skillID string) (*interfaces.GetSkillContentResponse, error) {
	fullURL := o.baseURL + fmt.Sprintf(getSkillContentURI, url.PathEscape(skillID))
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#GetSkillContent] URL: %s", fullURL)

	header := o.skillHeader(ctx, "operator.skill.content")
	code, respBody, err := o.httpClient.Get(ctx, fullURL, nil, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetSkillContent] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "技能内容接口调用失败", err)
	}

	var raw struct {
		SkillID string                        `json:"skill_id"`
		URL     string                        `json:"url"`
		Status  string                        `json:"status"`
		Files   []interfaces.SkillFileSummary `json:"files"`
	}
	if err = json.Unmarshal(utils.ObjectToByte(respBody), &raw); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetSkillContent] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("解析技能内容响应失败: %v", err))
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

// ReadSkillFile 读技能包内单个文件（同样两跳）。
func (o *operatorIntegrationClient) ReadSkillFile(ctx context.Context, req *interfaces.ReadSkillFileRequest) (*interfaces.ReadSkillFileResponse, error) {
	fullURL := o.baseURL + fmt.Sprintf(readSkillFileURI, url.PathEscape(req.SkillID))
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ReadSkillFile] URL: %s, RelPath: %s", fullURL, req.RelPath)

	header := o.skillHeader(ctx, "operator.skill.file_read")
	// 字段名是 rel_path，执行工厂侧 validate:"required"；发 path 会 400。
	code, respBody, err := o.httpClient.Post(ctx, fullURL, header, map[string]string{"rel_path": req.RelPath})
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ReadSkillFile] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "技能文件读取接口调用失败", err)
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
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("解析技能文件响应失败: %v", err))
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

// ExecuteSkill 在沙箱内执行技能入口命令。授权由执行工厂按账户强制（execute / public_access）。
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
		return nil, skillUpstreamError(ctx, code, "技能执行接口调用失败", err)
	}

	resp := &interfaces.ExecuteSkillResponse{}
	if err = json.Unmarshal(utils.ObjectToByte(respBody), resp); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ExecuteSkill] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("解析技能执行响应失败: %v", err))
	}
	if resp.SkillID == "" {
		resp.SkillID = req.SkillID
	}
	return resp, nil
}

// fetchSkillAsset 取对象存储上的技能文件正文。
// 这是发布态技能的第二跳：元数据接口只回 presigned URL，正文得自己取。
func (o *operatorIntegrationClient) fetchSkillAsset(ctx context.Context, assetURL string) ([]byte, error) {
	if assetURL == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusNotFound, "技能文件下载地址为空")
	}
	// presigned URL 自带签名，不能带上账户/追踪头（部分对象存储会把额外头计入签名校验）。
	code, body, err := o.httpClient.GetNoUnmarshal(ctx, assetURL, nil, nil)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#fetchSkillAsset] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, fmt.Sprintf("技能文件下载失败: %v", err))
	}
	if code < http.StatusOK || code >= http.StatusMultipleChoices {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, fmt.Sprintf("技能文件下载返回异常状态: %d", code))
	}
	if len(body) > maxSkillAssetBytes {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("技能文件超过 %d 字节上限，请改用技能包下载接口获取", maxSkillAssetBytes))
	}
	return body, nil
}

// skillHeader 组装出站头：账户身份 + 追踪上下文 + 业务域。
// 业务域是技能市场列表的必填项，取不到时退回默认业务域。
func (o *operatorIntegrationClient) skillHeader(ctx context.Context, operationName string) map[string]string {
	header := common.GetHeaderForChildOperation(ctx, operationName, 1)
	if bd := header[string(interfaces.HeaderXBusinessDomain)]; bd == "" {
		header[string(interfaces.HeaderXBusinessDomain)] = interfaces.DefaultBusinessDomainID
	}
	return header
}

// skillUpstreamError 把执行工厂的失败按其状态码归类。
//
// 下游的 4xx 是调用方参数错（路径越出技能包、技能不存在、无权限），照原码回给调用方；
// 一律翻成 502 会让模型以为服务坏了而不是自己传错，于是重试同样错误的参数。
// 其余（连不上、5xx）才是真的上游故障。
func skillUpstreamError(ctx context.Context, code int, action string, err error) error {
	if code >= http.StatusBadRequest && code < http.StatusInternalServerError {
		return infraErr.DefaultHTTPError(ctx, code, fmt.Sprintf("%s: %v", action, err))
	}
	return infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, fmt.Sprintf("%s: %v", action, err))
}

func firstNonEmptyStr(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
