// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
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
	// https://{host}:{port}/api/ontology-query/in/v1/knowledge-networks/:kn_id/object-types/:ot_id
	//
	// Query parameters are assembled with url.Values at call time rather than baked
	// into this format string: the set is no longer fixed (exclude_system_properties
	// repeats, ignoring_store_cache is conditional), and ot_id reaches us straight
	// from an agent, so an unescaped "?" or "&" in it would smuggle parameters into
	// the downstream request — the same hazard queryMetricDataURI documents below.
	queryObjectInstancesURI = "/in/v1/knowledge-networks/%s/object-types/%s"
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
	// https://{host}:{port}/api/ontology-query/in/v1/knowledge-networks/:kn_id/metrics/:metric_id/data
	//
	// Both ids are escaped at call time: metric_id arrives straight from an agent
	// (the tool schema constrains it to a string and nothing more), and an
	// unescaped one carrying "?" or "&" would smuggle query parameters — branch
	// and fill_null among them — into the downstream request.
	queryMetricDataURI = "/in/v1/knowledge-networks/%s/metrics/%s/data"
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

// rejectNilSortEntries turns a null element in `sort` into a 400 here instead of
// letting it become a panic downstream.
//
// A JSON `"sort": [null]` binds to []*SortSpec{nil} and is forwarded verbatim, and
// ontology-query dereferences the element without a nil guard in two places —
// driveradapters/validate.go (validateObjectSearchRequest reads sp.Field) and
// logics/common.go (BuildDslQuery reads sp.Field/sp.Direction). So external input
// crashes the downstream service rather than earning a 400.
//
// This is not the "validate half of it here" mistake the SortSpec comment warns
// about: whether a field belongs to an object type is downstream's knowledge, but a
// nil element is malformed structure that can produce no downstream verdict at all.
func rejectNilSortEntries(ctx context.Context, sort []*interfaces.SortSpec) error {
	for i, sp := range sort {
		if sp == nil {
			return infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
				fmt.Sprintf("sort[%d] 不能为 null，每个排序项都必须带 field 与 direction", i))
		}
	}
	return nil
}

func (o *ontologyQueryClient) QueryObjectInstances(ctx context.Context, req *interfaces.QueryObjectInstancesReq) (resp *interfaces.QueryObjectInstancesResp, err error) {
	if err = rejectNilSortEntries(ctx, req.Sort); err != nil {
		return nil, err
	}

	// Expand the flat `filters` sugar into a nested condition before forwarding.
	expandFilters(req)

	// need_total is a body field, not a query parameter, and it is not optional here:
	// without it ontology-query omits total_count, leaving the caller able to tell
	// "is there a next page" but not "how many matched at all".
	req.NeedTotal = true

	uri := fmt.Sprintf(queryObjectInstancesURI, url.PathEscape(req.KnID), url.PathEscape(req.OtID))
	query := url.Values{}
	query.Set("include_type_info", strconv.FormatBool(req.IncludeTypeInfo))
	query.Set("include_logic_params", strconv.FormatBool(req.IncludeLogicParams))
	if req.IgnoringStoreCache {
		query.Set("ignoring_store_cache", "true")
	}
	for _, prop := range req.ExcludeSystemProperties {
		query.Add("exclude_system_properties", prop)
	}
	target := fmt.Sprintf("%s%s?%s", o.baseURL, uri, query.Encode())

	header := common.GetHeaderForChildOperation(ctx, "ontology.object.query", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"
	_, respBody, err := o.httpClient.Post(ctx, target, header, req)
	if err != nil {
		o.logger.WithContext(ctx).Warnf("[OntologyQuery#QueryObjectInstances] QueryObjectInstances request failed, err: %v", err)
		err = classifyQueryError(ctx, err)
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
	resolveTotalCount(req, resp)
	return
}

// resolveTotalCount recovers the "zero hits" case that the wire format cannot carry.
//
// ontology-query's Objects.TotalCount is `json:"total_count,omitempty"`, so a real
// total of 0 is dropped before it ever reaches us — indistinguishable, in the body
// alone, from the total not having been computed. The request tells them apart:
// need_total is forced on for every call, and the only thing downstream does with it
// is turn it off when search_after is non-empty (logics/common.go BuildDslQuery). So
// with no cursor in the request, an absent total means the count ran and came back 0;
// with a cursor, it means no count ran and the caller must not read it as 0.
func resolveTotalCount(req *interfaces.QueryObjectInstancesReq, resp *interfaces.QueryObjectInstancesResp) {
	if resp == nil {
		return
	}
	hasCursor := req != nil && len(req.SearchAfter) > 0
	resp.TotalCount = resolveAbsentTotal(hasCursor, resp.TotalCount)
}

// resolveAbsentTotal 是上面那条推断的可复用形式：子图探索走的是同一条下游链路
// （起点对象类同样过 BuildDslQuery），三态语义必须与对象查询一字不差，否则同一个
// 「缺失」在两个工具里含义不同，比不做还糟。
func resolveAbsentTotal(hasCursor bool, total *int64) *int64 {
	if total != nil || hasCursor {
		return total
	}
	zero := int64(0)
	return &zero
}

// classifyQueryError re-classifies a downstream client error (4xx) from
// ontology-query. The shared HTTP client flattens every non-2xx into
// CommonExternalServerError ("调用依赖服务异常"), which mislabels a caller mistake
// — most notably a knn condition on a non-vector string field
// ("OntologyQuery.InvalidParameter.Condition: left field is not a vector field")
// — as a dependency outage, so the caller cannot tell "my query is wrong" from
// "the service is down".
//
// A 4xx is the caller's, so re-map it away from the dependency-error class while
// PRESERVING the downstream status code: a 400 stays a BadRequest, a 404 stays a
// NotFound (e.g. an unknown kn_id/ot_id), a 403 a Forbidden — collapsing them all
// to 400 would lose that distinction. The downstream detail is carried through so
// the caller sees the actionable cause. A 5xx genuinely IS a dependency fault, so
// it (and a transport error, which carries no HTTP code) stays as CommonExternalServerError.
//
// agent-retrieval deliberately does NOT pre-validate that a field is a vector
// field before forwarding: a property's condition_operations list is declared
// by the client at KN-build time and stored as-is (untrusted), so a field
// marked "knn" may still not be backed by a vector mapping, and vice versa. The
// downstream ontology-query verdict is the only authoritative signal, so the
// fix is to surface it clearly rather than guess here.
func classifyQueryError(ctx context.Context, err error) error {
	var he *infraErr.HTTPError
	if errors.As(err, &he) && he.HTTPCode >= http.StatusBadRequest && he.HTTPCode < http.StatusInternalServerError {
		details := he.ErrorDetails
		if details == nil {
			details = he.Error()
		}
		return infraErr.DefaultHTTPError(ctx, he.HTTPCode, details)
	}
	return err
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

	header := common.GetHeaderForChildOperation(ctx, "ontology.logic_property.query", 1)
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

	header := common.GetHeaderForChildOperation(ctx, "ontology.action.query", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"

	_, respBody, err := o.httpClient.Post(ctx, url, header, body)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#QueryActions] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "ActionQueryRequestFailed"))
	}

	resp = &interfaces.QueryActionsResponse{}
	resultByt := utils.ObjectToByte(respBody)
	err = json.Unmarshal(resultByt, resp)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#QueryActions] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "ActionQueryResponseInvalid"))
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

	// 记录请求日志（脱敏：dynamic_params 是任意工具输入，可能含令牌/密码等敏感值，
	// 仅记录参数名与实例数，不输出参数值。见 PR #379 review P1）
	dynamicParamKeys := make([]string, 0, len(req.DynamicParams))
	for k := range req.DynamicParams {
		dynamicParamKeys = append(dynamicParamKeys, k)
	}
	o.logger.WithContext(ctx).Debugf("[OntologyQuery#ExecuteActions] URL: %s, instances: %d, dynamic_param_keys: %v",
		url, len(req.InstanceIdentities), dynamicParamKeys)

	header := common.GetHeaderForChildOperation(ctx, "ontology.action.execute", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	_, respBody, err := o.httpClient.Post(ctx, url, header, body)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#ExecuteActions] Request failed, err: %v", err)
		// Preserve 4xx (e.g. 409 DuplicateExecution) so Agents see "duplicate rejected"
		// rather than a generic bad-gateway / silent empty success.
		return nil, classifyQueryError(ctx, err)
	}

	resp = &interfaces.ExecuteActionsResponse{}
	resultByt := utils.ObjectToByte(respBody)
	err = json.Unmarshal(resultByt, resp)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#ExecuteActions] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "ActionExecutionResponseInvalid"))
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

	header := common.GetHeaderForChildOperation(ctx, "ontology.action_execution.get", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	_, respBody, err := o.httpClient.Get(ctx, reqURL, nil, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#GetActionExecution] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "ActionExecutionLookupFailed"))
	}

	result := map[string]any{}
	resultByt := utils.ObjectToByte(respBody)
	if err = json.Unmarshal(resultByt, &result); err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#GetActionExecution] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "ActionExecutionLookupResponseInvalid"))
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
	// 游标分页：下游 action-logs 以逗号分隔字符串接收 search_after（GET query），
	// 故将上一页返回的 search_after 数组按元素拼成逗号分隔转发。
	if len(req.SearchAfter) > 0 {
		parts := make([]string, 0, len(req.SearchAfter))
		for _, v := range req.SearchAfter {
			parts = append(parts, fmt.Sprintf("%v", v))
		}
		query.Set("search_after", strings.Join(parts, ","))
	}
	// 默认返回总数，便于分页
	query.Set("need_total", "true")

	o.logger.WithContext(ctx).Debugf("[OntologyQuery#ListActionExecutions] URL: %s?%s", reqURL, query.Encode())

	header := common.GetHeaderForChildOperation(ctx, "ontology.action_execution.list", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	_, respBody, err := o.httpClient.Get(ctx, reqURL, query, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#ListActionExecutions] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "ActionExecutionHistoryRequestFailed"))
	}

	result := map[string]any{}
	resultByt := utils.ObjectToByte(respBody)
	if err = json.Unmarshal(resultByt, &result); err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#ListActionExecutions] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "ActionExecutionHistoryResponseInvalid"))
	}

	return result, nil
}

// ExploreSubgraph 起点探索式子图查询。
//
// 与 QueryInstanceSubgraph 打的是同一个下游端点，区别只在 query_type：那边固定
// relation_path（按调用方给的路径模板取数），这边**留空**，走下游默认的
// 「起点 + 方向 + 跳数」探索分支。留空不是省略——下游 handler 用
// c.DefaultQuery("query_type", "") 做 switch，空串就是探索模式的显式取值。
func (o *ontologyQueryClient) ExploreSubgraph(ctx context.Context, req *interfaces.ExploreSubgraphReq) (resp *interfaces.ExploreSubgraphResp, err error) {
	if err = rejectNilSortEntries(ctx, req.Sort); err != nil {
		return nil, err
	}

	// 与对象查询同理：need_total 不是调用方的选项，缺了它拿不到起点对象类的总数。
	req.NeedTotal = true

	uri := fmt.Sprintf(queryInstanceSubgraphURI, url.PathEscape(req.KnID))
	query := url.Values{}
	// query_type 显式留空：下游按空串选探索分支，写成 relation_path 就变成另一种模式。
	query.Set("query_type", "")
	query.Set("include_logic_params", strconv.FormatBool(req.IncludeLogicParams))
	if req.IgnoringStoreCache {
		query.Set("ignoring_store_cache", "true")
	}
	target := fmt.Sprintf("%s%s?%s", o.baseURL, uri, query.Encode())

	header := common.GetHeaderForChildOperation(ctx, "ontology.subgraph.explore", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	header["x-http-method-override"] = "GET"

	o.logger.WithContext(ctx).Debugf("[OntologyQuery#ExploreSubgraph] URL: %s", target)

	_, respBody, err := o.httpClient.Post(ctx, target, header, req)
	if err != nil {
		o.logger.WithContext(ctx).Warnf("[OntologyQuery#ExploreSubgraph] request failed, err: %v", err)
		// 起点对象类不存在、方向非法、path_length 超过 3 都是调用方的错，下游回 400/404
		// 且带可执行详情，复用对象查询那套分类，别塌陷成依赖故障。
		return nil, classifyQueryError(ctx, err)
	}

	resp = &interfaces.ExploreSubgraphResp{}
	resultByt := utils.ObjectToByte(respBody)
	if err = json.Unmarshal(resultByt, resp); err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#ExploreSubgraph] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "SubgraphQueryResponseInvalid"))
	}
	resp.TotalCount = resolveAbsentTotal(len(req.SearchAfter) > 0, resp.TotalCount)

	return resp, nil
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
	header := common.GetHeaderForChildOperation(ctx, "ontology.subgraph.query", 1)
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
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "SubgraphQueryResponseInvalid"))
		return nil, err
	}

	// 记录响应日志
	respJSON, _ := json.Marshal(resp)
	o.logger.WithContext(ctx).Debugf("[OntologyQuery#QueryInstanceSubgraph] Response: %s", string(respJSON))

	return resp, nil
}

// QueryMetricData 按指标自身口径计算取数（OT-first 路径第 3 步）。
//
// 走 ontology-query 内部路由，与 QueryLogicProperties 同一姿势：账户信息随 header
// 透传，授权在下游判定。
func (o *ontologyQueryClient) QueryMetricData(ctx context.Context, knID, metricID string, fillNull bool,
	req *interfaces.MetricQueryDownstreamReq) (resp *interfaces.MetricQueryDownstreamResp, err error) {
	uri := fmt.Sprintf(queryMetricDataURI, url.PathEscape(knID), url.PathEscape(metricID))
	query := url.Values{}
	query.Set("fill_null", strconv.FormatBool(fillNull))
	target := fmt.Sprintf("%s%s?%s", o.baseURL, uri, query.Encode())

	header := common.GetHeaderForChildOperation(ctx, "ontology.metric.query", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	if req == nil {
		req = &interfaces.MetricQueryDownstreamReq{}
	}
	_, respBody, err := o.httpClient.Post(ctx, target, header, req)
	if err != nil {
		o.logger.WithContext(ctx).Warnf("[OntologyQuery#QueryMetricData] kn=%s metric=%s failed: %v", knID, metricID, err)
		// 指标不存在 / 参数不合法是调用方的错，别混进依赖故障。
		return nil, classifyQueryError(ctx, err)
	}

	resp = &interfaces.MetricQueryDownstreamResp{}
	resultByt := utils.ObjectToByte(respBody)
	if err = json.Unmarshal(resultByt, resp); err != nil {
		o.logger.WithContext(ctx).Errorf("[OntologyQuery#QueryMetricData] Unmarshal %s err:%v", string(resultByt), err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	return resp, nil
}
