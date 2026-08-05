// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type bknBackendAccess struct {
	logger     interfaces.Logger
	baseURL    string
	httpClient interfaces.HTTPClient
}

var (
	bknAccessOnce sync.Once
	bknAccess     interfaces.BknBackendAccess
)

// NewBknBackendAccess 创建 BknBackendAccess
func NewBknBackendAccess() interfaces.BknBackendAccess {
	bknAccessOnce.Do(func() {
		conf := config.NewConfigLoader()
		bknAccess = &bknBackendAccess{
			logger:     conf.GetLogger(),
			baseURL:    conf.BknBackend.BuildURL("/api/bkn-backend"),
			httpClient: rest.NewHTTPClient(),
		}
	})
	return bknAccess
}

// ListKnowledgeNetworks 列出知识网络（GET /in/v1/knowledge-networks），用于让外部发现 kn_id。
func (b *bknBackendAccess) ListKnowledgeNetworks(ctx context.Context, req *interfaces.ListKnReq) (resp *interfaces.ListKnResp, err error) {
	src := fmt.Sprintf("%s/in/v1/knowledge-networks", b.baseURL)
	header := common.GetHeaderForChildOperation(ctx, "bkn.knowledge_network.list", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	queryValues := url.Values{}
	if req != nil {
		if req.NamePattern != "" {
			queryValues.Set("name_pattern", req.NamePattern)
		}
		if req.Limit > 0 {
			queryValues.Set("limit", strconv.Itoa(req.Limit))
		}
		if req.Offset > 0 {
			queryValues.Set("offset", strconv.Itoa(req.Offset))
		}
		if req.Sort != "" {
			queryValues.Set("sort", req.Sort)
		}
		if req.Direction != "" {
			queryValues.Set("direction", req.Direction)
		}
	}

	respCode, respBody, err := b.httpClient.GetNoUnmarshal(ctx, src, queryValues, header)
	if err != nil {
		b.logger.WithContext(ctx).Errorf("[BknBackendAccess] ListKnowledgeNetworks request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] ListKnowledgeNetworks request failed, err: %v", err))
	}

	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		b.logger.Errorf("[BknBackendAccess] ListKnowledgeNetworks get resp failed, [%s], %v\n", src, respBody)

		var baseError interfaces.KnBaseError
		if err := sonic.Unmarshal(respBody, &baseError); err != nil {
			b.logger.Errorf("unmarshal KnBaseError failed: %v\n", err)
			return nil, err
		}

		return nil, &infraErr.HTTPError{
			HTTPCode:     respCode,
			Code:         baseError.ErrorCode,
			Description:  baseError.Description,
			Solution:     baseError.Solution,
			ErrorLink:    baseError.ErrorLink,
			ErrorDetails: baseError.ErrorDetails,
		}
	}

	resp = &interfaces.ListKnResp{}
	if len(respBody) == 0 {
		return resp, nil
	}
	if err := sonic.Unmarshal(respBody, resp); err != nil {
		b.logger.Errorf("[BknBackendAccess] ListKnowledgeNetworks unmarshal response failed: %v\n", err)
		return nil, err
	}
	return resp, nil
}

// GetKnowledgeNetworkDetail 获取知识网络详情（include_detail=true, mode=export）
// 对应 Python 的 _get_knowledge_network_detail
func (b *bknBackendAccess) GetKnowledgeNetworkDetail(ctx context.Context, knID string) (*interfaces.KnowledgeNetworkDetail, error) {
	src := fmt.Sprintf("%s/in/v1/knowledge-networks/%s", b.baseURL, knID)
	header := common.GetHeaderForChildOperation(ctx, "bkn.knowledge_network.get", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	queryValues := url.Values{}
	queryValues.Set("include_detail", "true")
	queryValues.Set("mode", "export")

	respCode, respBody, err := b.httpClient.GetNoUnmarshal(ctx, src, queryValues, header)

	result := &interfaces.KnowledgeNetworkDetail{ID: knID}
	if err != nil {
		b.logger.WithContext(ctx).Errorf("[BknBackendAccess] GetKnowledgeNetworkDetail request failed, err: %v", err)
		return result, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] GetKnowledgeNetworkDetail request failed, err: %v", err))
	}

	if respCode == http.StatusNotFound && len(respBody) == 0 {
		b.logger.WithContext(ctx).Warnf("[BknBackendAccess] request not found, [%s]", src)
		return result, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] request not found, [%s]", src))
	}

	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		b.logger.Errorf("[BknBackendAccess] GetKnowledgeNetworkDetail get resp failed, [%s], %v\n", src, respBody)

		var baseError interfaces.KnBaseError
		if err := sonic.Unmarshal(respBody, &baseError); err != nil {
			b.logger.Errorf("unmarshal KnBaseError failed: %v\n", err)
			return result, err
		}

		return result, &infraErr.HTTPError{
			HTTPCode:     respCode,
			Code:         baseError.ErrorCode,
			Description:  baseError.Description,
			Solution:     baseError.Solution,
			ErrorLink:    baseError.ErrorLink,
			ErrorDetails: baseError.ErrorDetails,
		}
	}

	if len(respBody) == 0 {
		return result, nil
	}

	if err := sonic.Unmarshal(respBody, result); err != nil {
		b.logger.Errorf("[BknBackendAccess] GetKnowledgeNetworkDetail unmarshal failed: %v\n", err)
		return result, err
	}

	return result, nil
}

// SearchObjectTypes 搜索对象类
func (b *bknBackendAccess) SearchObjectTypes(ctx context.Context, query *interfaces.QueryConceptsReq) (objectTypes *interfaces.ObjectTypeConcepts, err error) {
	src := fmt.Sprintf("%s/in/v1/knowledge-networks/%s/object-types", b.baseURL, query.KnID)
	header := common.GetHeaderForChildOperation(ctx, "bkn.object_type.search", 1)
	header["Content-Type"] = "application/json"
	header["x-http-method-override"] = "GET"
	respCode, respBody, err := b.httpClient.PostNoUnmarshal(ctx, src, header, query)

	objectTypes = &interfaces.ObjectTypeConcepts{}
	if err != nil {
		b.logger.WithContext(ctx).Errorf("[BknBackendAccess] SearchObjectTypes request failed, err: %v", err)
		return objectTypes, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] SearchObjectTypes request failed, err: %v", err))
	}

	if respCode == http.StatusNotFound && len(respBody) == 0 {
		b.logger.WithContext(ctx).Warnf("[BknBackendAccess] request not found, [%s]", src)
		return objectTypes, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] request not found, [%s]", src))
	}

	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		b.logger.Errorf("[BknBackendAccess] SearchObjectTypes， get resp failed, [%s], %v\n", src, respBody)

		var baseError interfaces.KnBaseError
		if err := sonic.Unmarshal(respBody, &baseError); err != nil {
			b.logger.Errorf("unmalshal KnBaseError failed: %v\n", err)
			return objectTypes, err
		}

		return objectTypes, &infraErr.HTTPError{
			HTTPCode:     respCode,
			Code:         baseError.ErrorCode,
			Description:  baseError.Description,
			Solution:     baseError.Solution,
			ErrorLink:    baseError.ErrorLink,
			ErrorDetails: baseError.ErrorDetails,
		}
	}

	if len(respBody) == 0 {
		return objectTypes, nil
	}

	// 处理返回结果
	if err := sonic.Unmarshal(respBody, objectTypes); err != nil {
		b.logger.Errorf("[BknBackendAccess] SearchObjectTypes unmarshal ObjectTypes failed: %v\n", err)
		return nil, err
	}

	return objectTypes, nil
}

// GetObjectTypeDetail 获取对象类详情
func (b *bknBackendAccess) GetObjectTypeDetail(ctx context.Context, knID string, otIds []string, includeDetail bool) ([]*interfaces.ObjectType, error) {
	src := fmt.Sprintf("%s/in/v1/knowledge-networks/%s/object-types/%s", b.baseURL, knID, strings.Join(otIds, ","))
	header := common.GetHeaderForChildOperation(ctx, "bkn.object_type.get", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"
	queryValues := url.Values{}
	queryValues.Set("include_detail", strconv.FormatBool(includeDetail))

	respCode, respBody, err := b.httpClient.GetNoUnmarshal(ctx, src, queryValues, header)

	var emptyObjectTypes []*interfaces.ObjectType
	if err != nil {
		b.logger.WithContext(ctx).Errorf("[BknBackendAccess] GetObjectTypeDetail request failed, err: %v", err)
		return emptyObjectTypes, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] GetObjectTypeDetail request failed, err: %v", err))
	}

	if respCode == http.StatusNotFound && len(respBody) == 0 {
		b.logger.WithContext(ctx).Warnf("[BknBackendAccess] request not found, [%s]", src)
		return emptyObjectTypes, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] request not found, [%s]", src))
	}

	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		b.logger.Errorf("[BknBackendAccess] GetObjectTypeDetail get resp failed, [%s], %v\n", src, respBody)

		var baseError interfaces.KnBaseError
		if err := sonic.Unmarshal(respBody, &baseError); err != nil {
			b.logger.Errorf("unmalshal KnBaseError failed: %v\n", err)
			return emptyObjectTypes, err
		}

		return emptyObjectTypes, &infraErr.HTTPError{
			HTTPCode:     respCode,
			Code:         baseError.ErrorCode,
			Description:  baseError.Description,
			Solution:     baseError.Solution,
			ErrorLink:    baseError.ErrorLink,
			ErrorDetails: baseError.ErrorDetails,
		}
	}

	if len(respBody) == 0 {
		return emptyObjectTypes, nil
	}

	// 处理返回结果 - 适配新的响应结构 {"entries": []}
	var response struct {
		Entries []*interfaces.ObjectType `json:"entries"`
	}
	if err := sonic.Unmarshal(respBody, &response); err != nil {
		b.logger.Errorf("[BknBackendAccess]GetObjectTypeDetail unmalshal ObjectTypes failed: %v\n", err)
		return emptyObjectTypes, err
	}

	return response.Entries, nil
}

// SearchRelationTypes 搜索关系类
func (b *bknBackendAccess) SearchRelationTypes(ctx context.Context, query *interfaces.QueryConceptsReq) (releationTypes *interfaces.RelationTypeConcepts, err error) {
	src := fmt.Sprintf("%s/in/v1/knowledge-networks/%s/relation-types", b.baseURL, query.KnID)
	header := common.GetHeaderForChildOperation(ctx, "bkn.relation_type.search", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"
	respCode, respBody, err := b.httpClient.PostNoUnmarshal(ctx, src, header, query)
	if err != nil {
		b.logger.WithContext(ctx).Errorf("[BknBackendAccess] SearchRelationTypes request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] SearchRelationTypes request failed, err: %v", err))
	}

	if respCode == http.StatusNotFound && len(respBody) == 0 {
		b.logger.WithContext(ctx).Warnf("[BknBackendAccess] request not found, [%s]", src)
		return nil, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] request not found, [%s]", src))
	}

	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		b.logger.Errorf("[BknBackendAccess] SearchRelationTypes get resp failed, [%s], %v\n", src, respBody)

		var baseError interfaces.KnBaseError
		if err := sonic.Unmarshal(respBody, &baseError); err != nil {
			b.logger.Errorf("unmalshal KnBaseError failed: %v\n", err)
			return nil, err
		}

		return nil, &infraErr.HTTPError{
			HTTPCode:     respCode,
			Code:         baseError.ErrorCode,
			Description:  baseError.Description,
			Solution:     baseError.Solution,
			ErrorLink:    baseError.ErrorLink,
			ErrorDetails: baseError.ErrorDetails,
		}
	}

	releationTypes = &interfaces.RelationTypeConcepts{}
	if len(respBody) == 0 {
		return releationTypes, nil
	}

	// 处理返回结果
	if err := sonic.Unmarshal(respBody, releationTypes); err != nil {
		b.logger.Errorf("[BknBackendAccess]SearchRelationTypes unmalshal RelationTypes failed: %v\n", err)
		return nil, err
	}

	return releationTypes, nil
}

// GetRelationTypeDetail 获取关系类详情
func (b *bknBackendAccess) GetRelationTypeDetail(ctx context.Context, knID string, rtIDs []string, includeDetail bool) ([]*interfaces.RelationType, error) {
	src := fmt.Sprintf("%s/in/v1/knowledge-networks/%s/relation-types/%s", b.baseURL, knID, strings.Join(rtIDs, ","))
	header := common.GetHeaderForChildOperation(ctx, "bkn.relation_type.get", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"
	queryValues := url.Values{}
	queryValues.Set("include_detail", strconv.FormatBool(includeDetail))

	respCode, respBody, err := b.httpClient.GetNoUnmarshal(ctx, src, queryValues, header)

	var emptyRelationTypes []*interfaces.RelationType
	if err != nil {
		b.logger.WithContext(ctx).Errorf("[BknBackendAccess] GetRelationTypeDetail request failed, err: %v", err)
		return emptyRelationTypes, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] GetRelationTypeDetail request failed, err: %v", err))
	}

	if respCode == http.StatusNotFound && len(respBody) == 0 {
		b.logger.WithContext(ctx).Warnf("[BknBackendAccess] request not found, [%s]", src)
		return emptyRelationTypes, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] request not found, [%s]", src))
	}

	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		b.logger.Errorf("[BknBackendAccess] GetRelationTypeDetail get resp failed, [%s], %v\n", src, respBody)

		var baseError interfaces.KnBaseError
		if err := sonic.Unmarshal(respBody, &baseError); err != nil {
			b.logger.Errorf("unmalshal KnBaseError failed: %v\n", err)
			return emptyRelationTypes, err
		}

		return emptyRelationTypes, &infraErr.HTTPError{
			HTTPCode:     respCode,
			Code:         baseError.ErrorCode,
			Description:  baseError.Description,
			Solution:     baseError.Solution,
			ErrorLink:    baseError.ErrorLink,
			ErrorDetails: baseError.ErrorDetails,
		}
	}

	if len(respBody) == 0 {
		return emptyRelationTypes, nil
	}

	// 处理返回结果
	var releationTypes []*interfaces.RelationType
	if err := sonic.Unmarshal(respBody, &releationTypes); err != nil {
		b.logger.Errorf("[BknBackendAccess]GetRelationTypeDetail unmalshal releationTypes failed: %v\n", err)
		return emptyRelationTypes, err
	}

	return releationTypes, nil
}

// SearchActionTypes 搜索行动类
func (b *bknBackendAccess) SearchActionTypes(ctx context.Context, query *interfaces.QueryConceptsReq) (actionTypes *interfaces.ActionTypeConcepts, err error) {
	src := fmt.Sprintf("%s/in/v1/knowledge-networks/%s/action-types", b.baseURL, query.KnID)
	header := common.GetHeaderForChildOperation(ctx, "bkn.action_type.search", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"
	respCode, respBody, err := b.httpClient.PostNoUnmarshal(ctx, src, header, query)
	if err != nil {
		b.logger.WithContext(ctx).Errorf("[BknBackendAccess] SearchActionTypes request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] SearchActionTypes request failed, err: %v", err))
	}

	if respCode == http.StatusNotFound && len(respBody) == 0 {
		b.logger.WithContext(ctx).Warnf("[BknBackendAccess] request not found, [%s]", src)
		return nil, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] request not found, [%s]", src))
	}

	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		b.logger.Errorf("[BknBackendAccess] SearchActionTypes get resp failed, [%s], %v\n", src, respBody)

		var baseError interfaces.KnBaseError
		if err := sonic.Unmarshal(respBody, &baseError); err != nil {
			b.logger.Errorf("unmalshal KnBaseError failed: %v\n", err)
			return nil, err
		}

		return nil, &infraErr.HTTPError{
			HTTPCode:     respCode,
			Code:         baseError.ErrorCode,
			Description:  baseError.Description,
			Solution:     baseError.Solution,
			ErrorLink:    baseError.ErrorLink,
			ErrorDetails: baseError.ErrorDetails,
		}
	}

	actionTypes = &interfaces.ActionTypeConcepts{}
	if len(respBody) == 0 {
		return actionTypes, nil
	}

	// 处理返回结果
	if err := sonic.Unmarshal(respBody, actionTypes); err != nil {
		b.logger.Errorf("[BknBackendAccess]SearchActionTypes unmalshal actionTypes failed: %v\n", err)
		return nil, err
	}

	return actionTypes, nil
}

// SearchMetricTypes 搜索指标类
func (b *bknBackendAccess) SearchMetricTypes(ctx context.Context, query *interfaces.QueryConceptsReq) (metricTypes *interfaces.MetricTypeConcepts, err error) {
	src := fmt.Sprintf("%s/in/v1/knowledge-networks/%s/metrics", b.baseURL, query.KnID)
	header := common.GetHeaderForChildOperation(ctx, "bkn.metric.search", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"
	respCode, respBody, err := b.httpClient.PostNoUnmarshal(ctx, src, header, query)

	metricTypes = &interfaces.MetricTypeConcepts{}
	if err != nil {
		b.logger.WithContext(ctx).Errorf("[BknBackendAccess] SearchMetricTypes request failed, err: %v", err)
		return metricTypes, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] SearchMetricTypes request failed, err: %v", err))
	}

	if respCode == http.StatusNotFound && len(respBody) == 0 {
		b.logger.WithContext(ctx).Warnf("[BknBackendAccess] request not found, [%s]", src)
		return metricTypes, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] request not found, [%s]", src))
	}

	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		b.logger.Errorf("[BknBackendAccess] SearchMetricTypes get resp failed, [%s], %v\n", src, respBody)

		var baseError interfaces.KnBaseError
		if err := sonic.Unmarshal(respBody, &baseError); err != nil {
			b.logger.Errorf("unmalshal KnBaseError failed: %v\n", err)
			return nil, err
		}

		return nil, &infraErr.HTTPError{
			HTTPCode:     respCode,
			Code:         baseError.ErrorCode,
			Description:  baseError.Description,
			Solution:     baseError.Solution,
			ErrorLink:    baseError.ErrorLink,
			ErrorDetails: baseError.ErrorDetails,
		}
	}

	if len(respBody) == 0 {
		return metricTypes, nil
	}

	if err := sonic.Unmarshal(respBody, metricTypes); err != nil {
		b.logger.Errorf("[BknBackendAccess] SearchMetricTypes unmarshal metricTypes failed: %v\n", err)
		return nil, err
	}

	return metricTypes, nil
}

// GetActionTypeDetail 获取行动类详情
func (b *bknBackendAccess) GetActionTypeDetail(ctx context.Context, knID string, atIDs []string, includeDetail bool) ([]*interfaces.ActionType, error) {
	src := fmt.Sprintf("%s/in/v1/knowledge-networks/%s/action-types/%s", b.baseURL, knID, strings.Join(atIDs, ","))
	header := common.GetHeaderForChildOperation(ctx, "bkn.action_type.get", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"
	queryValues := url.Values{}
	queryValues.Set("include_detail", strconv.FormatBool(includeDetail))

	respCode, respBody, err := b.httpClient.GetNoUnmarshal(ctx, src, queryValues, header)

	var emptyActionTypes []*interfaces.ActionType
	if err != nil {
		b.logger.WithContext(ctx).Errorf("[BknBackendAccess] GetActionTypeDetail request failed, err: %v", err)
		return emptyActionTypes, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] GetActionTypeDetail request failed, err: %v", err))
	}

	if respCode == http.StatusNotFound && len(respBody) == 0 {
		b.logger.WithContext(ctx).Warnf("[BknBackendAccess] request not found, [%s]", src)
		return emptyActionTypes, infraErr.DefaultHTTPError(ctx, respCode,
			fmt.Sprintf("[BknBackendAccess] request not found, [%s]", src))
	}

	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		b.logger.Errorf("[BknBackendAccess] GetActionTypeDetail get resp failed, [%s], %v\n", src, respBody)

		var baseError interfaces.KnBaseError
		if err := sonic.Unmarshal(respBody, &baseError); err != nil {
			b.logger.Errorf("unmalshal KnBaseError failed: %v\n", err)
			return emptyActionTypes, err
		}

		return emptyActionTypes, &infraErr.HTTPError{
			HTTPCode:     respCode,
			Code:         baseError.ErrorCode,
			Description:  baseError.Description,
			Solution:     baseError.Solution,
			ErrorLink:    baseError.ErrorLink,
			ErrorDetails: baseError.ErrorDetails,
		}
	}

	if len(respBody) == 0 {
		return emptyActionTypes, nil
	}

	// 处理返回结果
	var actionTypes []*interfaces.ActionType
	if err := sonic.Unmarshal(respBody, &actionTypes); err != nil {
		b.logger.Errorf("[BknBackendAccess]GetActionTypeDetail unmalshal actionTypes failed: %v\n", err)
		return emptyActionTypes, err
	}

	return actionTypes, nil
}

// metricsListEntry is one entry of the bkn-backend GET .../metrics response.
type metricsListEntry struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Comment       string `json:"comment"`
	Unit          string `json:"unit"`
	UnitType      string `json:"unit_type"`
	MetricType    string `json:"metric_type"`
	ScopeType     string `json:"scope_type"`
	ScopeRef      string `json:"scope_ref"`
	TimeDimension *struct {
		Property string `json:"property"`
	} `json:"time_dimension"`
	AnalysisDimensions []struct {
		Name string `json:"name"`
	} `json:"analysis_dimensions"`
}

// metricsListResp is the bkn-backend GET .../metrics response.
type metricsListResp struct {
	Entries    []metricsListEntry `json:"entries"`
	TotalCount int64              `json:"total_count"`
}

const (
	// metricsPageSize is one request's worth of metrics; bkn-backend's own MAX_LIMIT.
	metricsPageSize = 1000
	// maxScopedMetrics bounds the whole paged walk. It is a runaway guard for a
	// knowledge network with an absurd metric count, not the expected ceiling.
	maxScopedMetrics = 10000
)

// ListMetricsByObjectTypes 枚举挂在给定对象类下的指标（scope_type=object_type）。
// 走 bkn-backend 指标注册表（GET .../metrics），不是概念索引语义召回：对象类要"看得见"
// 自己的指标，必须是全量且与库一致的。
func (b *bknBackendAccess) ListMetricsByObjectTypes(ctx context.Context, knID string, otIDs []string) ([]*interfaces.RelatedMetric, error) {
	scopeRefs := make([]string, 0, len(otIDs))
	seen := make(map[string]struct{}, len(otIDs))
	for _, id := range otIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		scopeRefs = append(scopeRefs, id)
	}
	if strings.TrimSpace(knID) == "" || len(scopeRefs) == 0 {
		return nil, nil
	}

	src := fmt.Sprintf("%s/in/v1/knowledge-networks/%s/metrics", b.baseURL, knID)
	header := common.GetHeaderForChildOperation(ctx, "bkn.metric.list", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	// Page rather than cap. A truncated answer is indistinguishable from "this
	// object type has no metrics", and that is precisely the state that sends an
	// agent back to run_sql — the behaviour this whole path exists to remove.
	entries := make([]metricsListEntry, 0, metricsPageSize)
	var total int64
	for offset := 0; offset < maxScopedMetrics; offset += metricsPageSize {
		queryValues := url.Values{}
		queryValues.Set("scope_type", "object_type")
		queryValues.Set("scope_ref", strings.Join(scopeRefs, ","))
		queryValues.Set("limit", strconv.Itoa(metricsPageSize))
		queryValues.Set("offset", strconv.Itoa(offset))

		respCode, respBody, err := b.httpClient.GetNoUnmarshal(ctx, src, queryValues, header)
		if err != nil {
			b.logger.WithContext(ctx).Warnf("[BknBackendAccess] ListMetricsByObjectTypes request failed, err: %v", err)
			return nil, infraErr.DefaultHTTPError(ctx, respCode,
				fmt.Sprintf("[BknBackendAccess] ListMetricsByObjectTypes request failed, err: %v", err))
		}

		if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
			b.logger.WithContext(ctx).Warnf("[BknBackendAccess] ListMetricsByObjectTypes failed, [%s], code %d", src, respCode)

			var baseError interfaces.KnBaseError
			if err := sonic.Unmarshal(respBody, &baseError); err != nil {
				return nil, infraErr.DefaultHTTPError(ctx, respCode,
					fmt.Sprintf("[BknBackendAccess] ListMetricsByObjectTypes failed, code %d", respCode))
			}
			return nil, &infraErr.HTTPError{
				HTTPCode:     respCode,
				Code:         baseError.ErrorCode,
				Description:  baseError.Description,
				Solution:     baseError.Solution,
				ErrorLink:    baseError.ErrorLink,
				ErrorDetails: baseError.ErrorDetails,
			}
		}

		if len(respBody) == 0 {
			break
		}

		parsed := &metricsListResp{}
		if err := sonic.Unmarshal(respBody, parsed); err != nil {
			b.logger.WithContext(ctx).Errorf("[BknBackendAccess] ListMetricsByObjectTypes unmarshal failed: %v", err)
			return nil, err
		}
		total = parsed.TotalCount
		entries = append(entries, parsed.Entries...)
		if len(parsed.Entries) < metricsPageSize || int64(len(entries)) >= parsed.TotalCount {
			break
		}
	}

	// The hard cap is a runaway guard, not a page size. If it ever bites, the
	// answer is incomplete and has to say so out loud.
	if total > int64(len(entries)) {
		b.logger.WithContext(ctx).Warnf(
			"[BknBackendAccess] ListMetricsByObjectTypes stopped at %d of %d metrics for kn=%s (%d object types); "+
				"object types beyond the cap are advertised without their metrics",
			len(entries), total, knID, len(scopeRefs))
	}

	metrics := make([]*interfaces.RelatedMetric, 0, len(entries))
	for _, e := range entries {
		// bkn-backend applies the scope filter, but an older backend that ignores the
		// query parameter would otherwise hand every metric of the network to every
		// object type. Cheap to re-check, and wrong-by-default if we do not.
		if _, ok := seen[e.ScopeRef]; !ok {
			continue
		}
		metric := &interfaces.RelatedMetric{
			ID:         e.ID,
			Name:       e.Name,
			Comment:    e.Comment,
			MetricType: e.MetricType,
			Unit:       e.Unit,
			UnitType:   e.UnitType,
			ScopeRef:   e.ScopeRef,
		}
		if e.TimeDimension != nil {
			metric.TimeDimension = e.TimeDimension.Property
		}
		for _, d := range e.AnalysisDimensions {
			if strings.TrimSpace(d.Name) != "" {
				metric.AnalysisDimensions = append(metric.AnalysisDimensions, d.Name)
			}
		}
		metrics = append(metrics, metric)
	}
	return metrics, nil
}
