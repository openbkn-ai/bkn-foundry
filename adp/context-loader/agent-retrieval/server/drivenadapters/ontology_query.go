// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/utils"
)

type ontologyQueryClient struct {
	logger     interfaces.Logger
	baseURL    string
	httpClient interfaces.HTTPClient
}

var (
	ontologyQueryOnce sync.Once
	ontologyQuery     interfaces.DrivenOntologyQuery
)

const (
	// https://{host}:{port}/api/ontology-query/in/v1/knowledge-networks/:kn_id/object-types/:ot_id?include_type_info=true
	queryObjectInstancesURI = "/in/v1/knowledge-networks/%s/object-types/%s?include_type_info=%v&include_logic_params=%v"
	// https://{host}:{port}/api/ontology-query/in/v1/knowledge-networks/:kn_id/object-types/:ot_id/properties
	queryLogicPropertiesURI = "/in/v1/knowledge-networks/%s/object-types/%s/properties"
	// https://{host}:{port}/api/ontology-query/v1/knowledge-networks/:kn_id/action-types/:at_id
	queryActionsURI = "/in/v1/knowledge-networks/%s/action-types/%s"
	// https://{host}:{port}/api/ontology-query/in/v1/knowledge-networks/:kn_id/action-types/:at_id/execute
	executeActionsURI = "/in/v1/knowledge-networks/%s/action-types/%s/execute"
	// https://{host}:{port}/api/ontology-query/in/v1/knowledge-networks/:kn_id/action-executions/:execution_id
	getActionExecutionURI = "/in/v1/knowledge-networks/%s/action-executions/%s"
	// https://{host}:{port}/api/ontology-query/in/v1/knowledge-networks/:kn_id/action-logs
	listActionExecutionsURI = "/in/v1/knowledge-networks/%s/action-logs"
	// https://{host}:{port}/api/ontology-query/in/v1/knowledge-networks/:kn_id/subgraph
	queryInstanceSubgraphURI = "/in/v1/knowledge-networks/%s/subgraph"
)

// NewOntologyQueryAccess 创建OntologyQueryAccess
func NewOntologyQueryAccess() interfaces.DrivenOntologyQuery {
	ontologyQueryOnce.Do(func() {
		configLoader := config.NewConfigLoader()
		ontologyQuery = &ontologyQueryClient{
			logger:     configLoader.GetLogger(),
			baseURL:    configLoader.OntologyQuery.BuildURL("/api/ontology-query"),
			httpClient: rest.NewHTTPClient(),
		}
	})
	return ontologyQuery
}

// QueryObjectInstances 检索指定对象类的对象的详细数据
// expandFilters converts the flat Filters shortcut into a nested AND
// condition so downstream only ever sees `condition`. It is a no-op when
// there are no filters. When condition is already set it leaves condition
// untouched (condition wins), but it always clears Filters so the sugar
// field is never forwarded to ontology-query. Each filter becomes a leaf
// with value_from=const.
func expandFilters(req *interfaces.QueryObjectInstancesReq) {
	if len(req.Filters) == 0 {
		return
	}
	if req.Cond == nil {
		subs := make([]*interfaces.KnCondition, 0, len(req.Filters))
		for _, f := range req.Filters {
			subs = append(subs, &interfaces.KnCondition{
				Field:     f.Field,
				Operation: f.Op,
				Value:     f.Value,
				ValueFrom: interfaces.CondValueFromConst,
			})
		}
		req.Cond = &interfaces.KnCondition{
			Operation:     interfaces.KnOperationTypeAnd,
			SubConditions: subs,
		}
	}
	req.Filters = nil
}

func (o *ontologyQueryClient) QueryObjectInstances(ctx context.Context, req *interfaces.QueryObjectInstancesReq) (resp *interfaces.QueryObjectInstancesResp, err error) {
	// Expand the flat `filters` sugar into a nested condition before forwarding.
	expandFilters(req)

	uri := fmt.Sprintf(queryObjectInstancesURI, req.KnID, req.OtID, req.IncludeTypeInfo, req.IncludeLogicParams)
	url := fmt.Sprintf("%s%s", o.baseURL, uri)
	header := common.GetHeaderFromCtx(ctx)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"
	_, respBody, err := o.httpClient.Post(ctx, url, header, req)
	if err != nil {
		o.logger.WithContext(ctx).Warnf("[OntologyQuery#QueryObjectInstances] QueryObjectInstances request failed, err: %v", err)
		return
	}
	resp = &interfaces.QueryObjectInstancesResp{}
	resultByt := utils.ObjectToByte(respBody)
	err = json.Unmarshal(resultByt, resp)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#QueryObjectInstances] Unmarshal %s err:%v", string(resultByt), err)
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	return
}

// QueryLogicProperties 查询逻辑属性值
func (o *ontologyQueryClient) QueryLogicProperties(ctx context.Context, req *interfaces.QueryLogicPropertiesReq) (resp *interfaces.QueryLogicPropertiesResp, err error) {
	uri := fmt.Sprintf(queryLogicPropertiesURI, req.KnID, req.OtID)
	url := fmt.Sprintf("%s%s", o.baseURL, uri)

	// 构建请求体
	body := map[string]any{
		"_instance_identities": req.InstanceIdentities,
		"properties":           req.Properties,
		"dynamic_params":       req.DynamicParams,
	}

	// 📤 记录调用 ontology-query 的完整入参
	bodyJSON, _ := json.Marshal(body)
	o.logger.WithContext(ctx).Debugf("  ├─ [ontology-query 调用] URL: %s", url)
	o.logger.WithContext(ctx).Debugf("  ├─ [ontology-query 请求] Body: %s", string(bodyJSON))

	header := common.GetHeaderFromCtx(ctx)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"

	_, respBody, err := o.httpClient.Post(ctx, url, header, body)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("  └─ [ontology-query 响应] ❌ 请求失败: %v", err)
		return nil, err
	}

	resp = &interfaces.QueryLogicPropertiesResp{}
	resultByt := utils.ObjectToByte(respBody)
	err = json.Unmarshal(resultByt, resp)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("  └─ [ontology-query 响应] ❌ JSON 解析失败: %v", err)
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return nil, err
	}

	// 📥 记录 ontology-query 的完整出参
	respJSON, _ := json.Marshal(resp)
	o.logger.WithContext(ctx).Debugf("  └─ [ontology-query 响应] ✅ 成功 (%d 条数据): %s", len(resp.Datas), string(respJSON))
	return resp, nil
}

// QueryActions 查询行动
func (o *ontologyQueryClient) QueryActions(ctx context.Context, req *interfaces.QueryActionsRequest) (resp *interfaces.QueryActionsResponse, err error) {
	uri := fmt.Sprintf(queryActionsURI, req.KnID, req.AtID)
	url := fmt.Sprintf("%s%s", o.baseURL, uri)

	// 构建请求体
	body := map[string]any{
		"_instance_identities": req.InstanceIdentities,
	}

	// 记录请求日志
	bodyJSON, _ := json.Marshal(body)
	o.logger.WithContext(ctx).Debugf("[OntologyQuery#QueryActions] URL: %s", url)
	o.logger.WithContext(ctx).Debugf("[OntologyQuery#QueryActions] Request Body: %s", string(bodyJSON))

	header := common.GetHeaderFromCtx(ctx)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"

	_, respBody, err := o.httpClient.Post(ctx, url, header, body)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#QueryActions] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, fmt.Sprintf("行动查询接口调用失败: %v", err))
	}

	resp = &interfaces.QueryActionsResponse{}
	resultByt := utils.ObjectToByte(respBody)
	err = json.Unmarshal(resultByt, resp)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#QueryActions] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("解析行动查询响应失败: %v", err))
		return nil, err
	}

	// 记录响应日志
	respJSON, _ := json.Marshal(resp)
	o.logger.WithContext(ctx).Debugf("[OntologyQuery#QueryActions] Response: %s", string(respJSON))

	return resp, nil
}

// ExecuteActions 执行行动（异步），返回 execution_id
func (o *ontologyQueryClient) ExecuteActions(ctx context.Context, req *interfaces.ExecuteActionsRequest) (resp *interfaces.ExecuteActionsResponse, err error) {
	uri := fmt.Sprintf(executeActionsURI, req.KnID, req.AtID)
	url := fmt.Sprintf("%s%s", o.baseURL, uri)

	// 构建请求体（真实执行，携带动态参数；不设 method-override，为真正的 POST）
	body := map[string]any{
		"_instance_identities": req.InstanceIdentities,
	}
	if len(req.DynamicParams) > 0 {
		body["dynamic_params"] = req.DynamicParams
	}

	// 记录请求日志
	bodyJSON, _ := json.Marshal(body)
	o.logger.WithContext(ctx).Debugf("[OntologyQuery#ExecuteActions] URL: %s", url)
	o.logger.WithContext(ctx).Debugf("[OntologyQuery#ExecuteActions] Request Body: %s", string(bodyJSON))

	header := common.GetHeaderFromCtx(ctx)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	_, respBody, err := o.httpClient.Post(ctx, url, header, body)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#ExecuteActions] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, fmt.Sprintf("行动执行接口调用失败: %v", err))
	}

	resp = &interfaces.ExecuteActionsResponse{}
	resultByt := utils.ObjectToByte(respBody)
	err = json.Unmarshal(resultByt, resp)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#ExecuteActions] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("解析行动执行响应失败: %v", err))
		return nil, err
	}

	// 记录响应日志
	respJSON, _ := json.Marshal(resp)
	o.logger.WithContext(ctx).Debugf("[OntologyQuery#ExecuteActions] Response: %s", string(respJSON))

	return resp, nil
}

// GetActionExecution 查询单次行动执行的状态与结果
func (o *ontologyQueryClient) GetActionExecution(ctx context.Context, req *interfaces.GetActionExecutionRequest) (map[string]any, error) {
	uri := fmt.Sprintf(getActionExecutionURI, req.KnID, req.ExecutionID)
	reqURL := fmt.Sprintf("%s%s", o.baseURL, uri)

	o.logger.WithContext(ctx).Debugf("[OntologyQuery#GetActionExecution] URL: %s", reqURL)

	header := common.GetHeaderFromCtx(ctx)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	_, respBody, err := o.httpClient.Get(ctx, reqURL, nil, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#GetActionExecution] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, fmt.Sprintf("行动执行查询接口调用失败: %v", err))
	}

	result := map[string]any{}
	resultByt := utils.ObjectToByte(respBody)
	if err = json.Unmarshal(resultByt, &result); err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#GetActionExecution] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("解析行动执行查询响应失败: %v", err))
	}

	return result, nil
}

// ListActionExecutions 列出行动执行历史（可过滤，分页）
func (o *ontologyQueryClient) ListActionExecutions(ctx context.Context, req *interfaces.ListActionExecutionsRequest) (map[string]any, error) {
	uri := fmt.Sprintf(listActionExecutionsURI, req.KnID)
	reqURL := fmt.Sprintf("%s%s", o.baseURL, uri)

	query := url.Values{}
	if req.ActionTypeID != "" {
		query.Set("action_type_id", req.ActionTypeID)
	}
	if req.Status != "" {
		query.Set("status", req.Status)
	}
	if req.TriggerType != "" {
		query.Set("trigger_type", req.TriggerType)
	}
	if req.StartTimeFrom > 0 {
		query.Set("start_time_from", strconv.FormatInt(req.StartTimeFrom, 10))
	}
	if req.StartTimeTo > 0 {
		query.Set("start_time_to", strconv.FormatInt(req.StartTimeTo, 10))
	}
	if req.Offset > 0 {
		query.Set("offset", strconv.Itoa(req.Offset))
	}
	if req.Limit > 0 {
		query.Set("limit", strconv.Itoa(req.Limit))
	}
	// 默认返回总数，便于分页
	query.Set("need_total", "true")

	o.logger.WithContext(ctx).Debugf("[OntologyQuery#ListActionExecutions] URL: %s?%s", reqURL, query.Encode())

	header := common.GetHeaderFromCtx(ctx)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	_, respBody, err := o.httpClient.Get(ctx, reqURL, query, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#ListActionExecutions] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, fmt.Sprintf("行动执行历史查询接口调用失败: %v", err))
	}

	result := map[string]any{}
	resultByt := utils.ObjectToByte(respBody)
	if err = json.Unmarshal(resultByt, &result); err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#ListActionExecutions] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("解析行动执行历史查询响应失败: %v", err))
	}

	return result, nil
}

// QueryInstanceSubgraph 查询对象子图
func (o *ontologyQueryClient) QueryInstanceSubgraph(ctx context.Context, req *interfaces.QueryInstanceSubgraphReq) (resp *interfaces.QueryInstanceSubgraphResp, err error) {
	// 构建查询参数 - QueryType 固定为 "relation_path"
	queryParams := []string{}
	if req.IncludeLogicParams {
		queryParams = append(queryParams, fmt.Sprintf("include_logic_params=%v", req.IncludeLogicParams))
	}
	// 固定 query_type 为 relation_path
	queryParams = append(queryParams, "query_type=relation_path")

	queryStr := ""
	if len(queryParams) > 0 {
		queryStr = "?" + queryParams[0]
		for i := 1; i < len(queryParams); i++ {
			queryStr += "&" + queryParams[i]
		}
	}

	uri := fmt.Sprintf(queryInstanceSubgraphURI, req.KnID) + queryStr
	url := fmt.Sprintf("%s%s", o.baseURL, uri)

	// 构建请求体 - 直接透传 RelationTypePaths (any)
	body := map[string]any{
		"relation_type_paths": req.RelationTypePaths,
	}

	// 记录请求日志
	bodyJSON, _ := json.Marshal(body)
	o.logger.WithContext(ctx).Debugf("[OntologyQuery#QueryInstanceSubgraph] URL: %s", url)
	o.logger.WithContext(ctx).Debugf("[OntologyQuery#QueryInstanceSubgraph] Request Body: %s", string(bodyJSON))

	// 构建请求头
	header := common.GetHeaderFromCtx(ctx)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"

	// 发送请求
	_, respBody, err := o.httpClient.Post(ctx, url, header, body)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#QueryInstanceSubgraph] Request failed, err: %v", err)
		return nil, err
	}

	// 解析响应 - 直接解析到 any
	resp = &interfaces.QueryInstanceSubgraphResp{}
	resultByt := utils.ObjectToByte(respBody)
	err = json.Unmarshal(resultByt, resp)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#QueryInstanceSubgraph] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("解析子图查询响应失败: %v", err))
		return nil, err
	}

	// 记录响应日志
	respJSON, _ := json.Marshal(resp)
	o.logger.WithContext(ctx).Debugf("[OntologyQuery#QueryInstanceSubgraph] Response: %s", string(respJSON))

	return resp, nil
}
