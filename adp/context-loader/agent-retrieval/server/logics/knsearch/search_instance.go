// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/creasty/defaults"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	aerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// SearchInstance 用一句自然语言召回实例：概念召回锁定对象类，再在这些对象类上做
// 语义实例召回，回实例行与读懂它们所需的对象类定义。
func (s *knSearchService) SearchInstance(
	ctx context.Context,
	req *interfaces.SearchInstanceReq,
) (*interfaces.SearchInstanceResp, error) {
	knReq, err := NormalizeSearchInstanceReq(req)
	if err != nil {
		return nil, aerrors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
	}

	resp, err := s.KnSearch(ctx, knReq)
	if err != nil {
		return nil, err
	}

	out := FilterSearchInstanceResp(resp, boolValue(req.IncludeObjectTypes))
	bkntrace.EmitSearchInstanceEvents(ctx, s.Logger, req, out)
	return out, nil
}

// NormalizeSearchInstanceReq 把 SearchInstanceReq 转成 KnSearchReq。
//
// 与 NormalizeSearchSchemaReq 相对：那边写死 only_schema=true 只要 Schema，这边写死
// false 以进入语义实例召回——这正是本工具存在的理由。
func NormalizeSearchInstanceReq(req *interfaces.SearchInstanceReq) (*interfaces.KnSearchReq, error) {
	if req == nil {
		return nil, errors.New("request is required")
	}
	if err := defaults.Set(req); err != nil {
		return nil, errors.New("failed to apply defaults: " + err.Error())
	}

	knID := strings.TrimSpace(req.XKnID)
	if knID == "" {
		knID = strings.TrimSpace(req.KnID)
	}
	if knID == "" {
		return nil, errors.New("kn_id is required (configure X-Kn-ID header or pass kn_id in body)")
	}
	if strings.TrimSpace(req.Query) == "" {
		return nil, errors.New("query is required")
	}
	if req.MaxObjectTypes == nil || *req.MaxObjectTypes <= 0 {
		return nil, errors.New("max_object_types must be greater than 0")
	}
	if req.MaxInstancesPerType == nil || *req.MaxInstancesPerType <= 0 {
		return nil, errors.New("max_instances_per_type must be greater than 0")
	}

	onlySchema := false
	return &interfaces.KnSearchReq{
		XAccountID:   req.XAccountID,
		XAccountType: req.XAccountType,
		Query:        req.Query,
		KnID:         knID,
		OnlySchema:   &onlySchema,
		IndexOpsOnly: req.IndexOpsOnly,
		// 这里刻意用**本地**配置结构体（字段是 *bool）而不是请求面的
		// RetrievalConfig（字段是 bool 值类型）。请求面那条转换路径
		// （retrievalConfigStructToLocal）会把每个 bool 无条件包成 boolPtr，
		// 于是「没填」和「填了 false」变成同一件事，未设置的开关会以显式 false
		// 覆盖掉默认的 true——enable_global_final_score_ratio_filter、
		// enable_coarse_recall、enable_property_brief 三个默认开启的旋钮都会被静默关掉。
		// 走本地结构体则未设字段保持 nil，MergeRetrievalConfig 原样保留默认值。
		RetrievalConfig: &interfaces.KnSearchRetrievalConfig{
			ConceptRetrieval: &interfaces.KnSearchConceptRetrievalConfig{
				ConceptGroups: normalizeConceptGroups(req.ConceptGroups),
				TopK:          *req.MaxObjectTypes,
				// schema_brief 恒开：概念召回的 Schema 在这条路上只是实例召回的
				// 中间产物，调用方拿不到它，没有理由为它多付体积与下游查询开销。
				SchemaBrief: boolPtr(true),
			},
			SemanticInstanceRetrieval: &interfaces.KnSearchSemanticInstanceRetrievalConfig{
				PerTypeInstanceLimit: *req.MaxInstancesPerType,
			},
		},
	}, nil
}

// FilterSearchInstanceResp 从 KnSearchResp 里取实例，并按需附上读懂它们所需的对象类定义。
//
// 关系类与行动类一律丢弃：它们与「这几行数据怎么读」无关。
func FilterSearchInstanceResp(resp *interfaces.KnSearchResp, includeObjectTypes bool) *interfaces.SearchInstanceResp {
	out := &interfaces.SearchInstanceResp{Nodes: []any{}}
	if resp == nil {
		return out
	}
	out.Nodes = toAnySlice(resp.Nodes)
	if includeObjectTypes {
		out.ObjectTypes = objectTypesOfNodes(toAnySlice(resp.ObjectTypes), out.Nodes)
	}
	// message 只在没有命中时才有意义：有结果还带一句说明纯属噪音。
	if len(out.Nodes) == 0 && resp.Message != nil {
		out.Message = strings.TrimSpace(*resp.Message)
	}
	return out
}

// objectTypesOfNodes 只留真正出了实例的那些对象类。
//
// 概念召回扫过的对象类动辄几十个（VM 实测一次 20 个里只有 3 个出实例），
// 全回等于把 search_schema 的输出复制过来，那正是这个工具不该做的事。
func objectTypesOfNodes(objectTypes, nodes []any) []any {
	if len(objectTypes) == 0 || len(nodes) == 0 {
		return nil
	}
	hit := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		nodeMap, ok := node.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := nodeMap["object_type_id"].(string); strings.TrimSpace(id) != "" {
			hit[strings.TrimSpace(id)] = struct{}{}
		}
	}
	if len(hit) == 0 {
		return nil
	}

	kept := make([]any, 0, len(hit))
	for _, ot := range objectTypes {
		otMap, ok := ot.(map[string]any)
		if !ok {
			continue
		}
		id, _ := otMap["concept_id"].(string)
		if _, ok := hit[strings.TrimSpace(id)]; ok {
			kept = append(kept, ot)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}
