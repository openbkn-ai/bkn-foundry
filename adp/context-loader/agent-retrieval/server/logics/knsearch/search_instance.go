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

// SearchInstance uses a natural language sentence to recall instances: concept recall locks object types, and then performs operations on these object types.
// Semantic instance recall returns instance rows and the object type definitions required to read them.
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

// NormalizeSearchInstanceReq Converts SearchInstanceReq to KnSearchReq.
//
// Relative to NormalizeSearchSchemaReq: only_schema=true is hardcoded over there, only Schema is required, and hardcoded here.
// false to enter semantic instance recall - which is the raison d'être of this tool.
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
		// The **local** configuration structure is deliberately used here (the field is *bool) instead of the request side.
		// RetrievalConfig (field is of bool value type). Request the conversion path.
		// (retrievalConfigStructToLocal) will unconditionally wrap each bool into boolPtr,
		// So "not filled in" and "filled in false" become the same thing, and unset switches will be explicitly false.
		// Override the default true——enable_global_final_score_ratio_filter,
		// The three knobs enable_coarse_recall and enable_property_brief that are turned on by default will be turned off silently.
		// If the local structure is used, the unset fields will remain nil, and MergeRetrievalConfig will retain the default value as it is.
		RetrievalConfig: &interfaces.KnSearchRetrievalConfig{
			ConceptRetrieval: &interfaces.KnSearchConceptRetrievalConfig{
				ConceptGroups: normalizeConceptGroups(req.ConceptGroups),
				TopK:          *req.MaxObjectTypes,
				// schema_brief Hengkai: Schema recalled by concepts is only recalled by instances on this path.
				// The caller cannot get the intermediate product, so there is no reason to pay extra volume and downstream query overhead for it.
				SchemaBrief: boolPtr(true),
			},
			SemanticInstanceRetrieval: &interfaces.KnSearchSemanticInstanceRetrievalConfig{
				PerTypeInstanceLimit: *req.MaxInstancesPerType,
				InstanceRerankMode:   instanceRerankModeFromFlag(req.Rerank),
			},
		},
	}, nil
}

// FilterSearchInstanceResp takes instances from KnSearchResp and optionally attaches the object type definitions required to read them.
//
// Relationship classes and action classes are discarded: they have nothing to do with "how to read these rows of data".
func FilterSearchInstanceResp(resp *interfaces.KnSearchResp, includeObjectTypes bool) *interfaces.SearchInstanceResp {
	out := &interfaces.SearchInstanceResp{Nodes: []any{}}
	if resp == nil {
		return out
	}
	out.Nodes = toAnySlice(resp.Nodes)
	if includeObjectTypes {
		out.ObjectTypes = objectTypesOfNodes(toAnySlice(resp.ObjectTypes), out.Nodes)
	}
	// message is only meaningful when there is no hit: if there is a result, it is just noise.
	if len(out.Nodes) == 0 && resp.Message != nil {
		out.Message = strings.TrimSpace(*resp.Message)
	}
	return out
}

// objectTypesOfNodes only retains those object types that actually have instances.
//
// There are often dozens of object types scanned by concept recall (only 3 out of 20 instances were found in a VM measurement).
// Full return is equivalent to copying the output of search_schema, which is exactly what this tool is not supposed to do.
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

// instanceRerankModeFromFlag maps the switch on the tool surface to the fine ranking position.
//
// The tool parameters are only on/off: shadow is an operation and maintenance file used for online traffic forensics, and has no meaning for the Agent.
// Leave kn_search's retrieval_config to go.
func instanceRerankModeFromFlag(rerank *bool) string {
	if rerank != nil && *rerank {
		return InstanceRerankModeOn
	}
	return InstanceRerankModeOff
}
