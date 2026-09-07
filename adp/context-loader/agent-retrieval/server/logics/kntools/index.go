// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kntools exposes the Function tools a knowledge network has mounted to an MCP client:
// search_tools finds them, execute_tool runs one.
//
// Scope is the knowledge network's Function bindings, not the caller's whole visible catalogue.
// Both are needed and they are intersected: the bindings say which tools this network works with,
// and the caller's own permissions still decide whether a bound tool can be listed or run. Before
// this, the two halves of the same MCP session disagreed — Skills narrowed to what the network
// had mounted while tools stayed at everything the account could see.
package kntools

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/permission"
)

const (
	// defaultSearchLimit bounds what one search spends of a model's context: a
	// hit carries its trimmed input schema, so ten hits are ten OpenAPI bodies.
	defaultSearchLimit = 20
	maxSearchLimit     = 100
	// toolboxFanoutConcurrency bounds the catalogue walk. One request per
	// visible toolbox is the shape until a tool dataset exists; keep the burst
	// off Execution Factory small enough that a wide account cannot stall it.
	toolboxFanoutConcurrency = 5
)

// SearchToolsReq is the input for search_tools.
type SearchToolsReq struct {
	// KnID is required. Without it there is no scope to narrow to, and answering anyway would
	// return every tool the account can see — the behaviour this replaced.
	KnID      string `json:"kn_id"`
	Query     string `json:"query"`      // Optional. Ranks the mounted tools by name and description.
	ToolboxID string `json:"toolbox_id"` // Optional. Restricts the search to one toolbox's mounted tools.
	Limit     int    `json:"limit"`      // Optional. Caps returned tools, default 20, max 100.
}

// ToolEntry is one callable published Function tool.
type ToolEntry struct {
	ToolID      string         `json:"tool_id"`
	ToolboxID   string         `json:"toolbox_id"`
	ToolboxName string         `json:"toolbox_name,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	UseRule     string         `json:"use_rule,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// SearchToolsResp is the search_tools result.
type SearchToolsResp struct {
	Tools        []ToolEntry `json:"tools"`
	TotalMatched int         `json:"total_matched"`
	Truncated    bool        `json:"truncated,omitempty"`
	Message      string      `json:"message,omitempty"`
}

// ExecuteToolReq is the input for execute_tool.
type ExecuteToolReq struct {
	// KnID is required and is checked against the bindings before the call runs. Discovery being
	// narrowed is not a control on its own: a tool id can be held from an earlier session, or
	// guessed, and execution is the half that changes the world.
	KnID      string         `json:"kn_id"`
	ToolboxID string         `json:"toolbox_id"`
	ToolID    string         `json:"tool_id"`
	Arguments map[string]any `json:"arguments"`
}

// KnToolsService is the published Function tool surface.
type KnToolsService interface {
	SearchTools(ctx context.Context, req *SearchToolsReq) (*SearchToolsResp, error)
	ExecuteTool(ctx context.Context, req *ExecuteToolReq) (map[string]any, error)
}

type knToolsService struct {
	operator   interfaces.DrivenOperatorIntegration
	bknBackend interfaces.BknBackendAccess
	knAuthz    interfaces.KnowledgeNetworkAuthorizer
}

var (
	once    sync.Once
	service KnToolsService
)

// NewKnToolsService creates the KnToolsService singleton.
func NewKnToolsService() KnToolsService {
	once.Do(func() {
		conf := config.NewConfigLoader()
		service = &knToolsService{
			operator:   drivenadapters.NewOperatorIntegrationClient(),
			bknBackend: drivenadapters.NewBknBackendAccess(),
			knAuthz:    permission.NewKnowledgeNetworkAuthorizer(conf),
		}
	})
	return service
}

// NewKnToolsServiceWith builds a service over explicit driven adapters.
func NewKnToolsServiceWith(operator interfaces.DrivenOperatorIntegration,
	bknBackend interfaces.BknBackendAccess,
	knAuthz interfaces.KnowledgeNetworkAuthorizer) KnToolsService {
	return &knToolsService{operator: operator, bknBackend: bknBackend, knAuthz: knAuthz}
}

// SearchTools returns the Function tools this knowledge network has mounted, ranked against a
// query when one is given.
func (s *knToolsService) SearchTools(ctx context.Context, req *SearchToolsReq) (*SearchToolsResp, error) {
	if req == nil || strings.TrimSpace(req.KnID) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "ToolScopeKnIDRequired"))
	}
	limit := req.Limit
	if limit < 1 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	refs, err := s.boundToolRefs(ctx, strings.TrimSpace(req.KnID), strings.TrimSpace(req.ToolboxID))
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return &SearchToolsResp{
			Tools:   []ToolEntry{},
			Message: infraErr.LocalizedDetail(ctx, "NoBoundToolsInNetwork"),
		}, nil
	}

	hits, err := s.operator.SearchBoundTools(ctx, &interfaces.SearchBoundToolsRequest{
		Query:    strings.TrimSpace(req.Query),
		ToolRefs: refs,
		TopK:     limit,
	})
	if err != nil {
		return nil, err
	}

	matched := s.describeHits(ctx, hits, limit)
	resp := &SearchToolsResp{Tools: matched, TotalMatched: len(hits)}
	if len(hits) > len(matched) {
		resp.Truncated = true
		resp.Message = infraErr.LocalizedDetail(ctx, "ToolSearchTruncated")
	}
	if len(resp.Tools) == 0 {
		resp.Tools = []ToolEntry{}
		resp.Message = infraErr.LocalizedDetail(ctx, "NoPublishedToolsMatched")
	}
	return resp, nil
}

// boundToolRefs returns the network's Function bindings as "{box_id}/{tool_id}" references.
//
// A failure to read them fails the search. Continuing with an empty whitelist would look like a
// network that mounted nothing, and continuing without one would return the whole catalogue —
// both answer a question the service cannot currently answer.
func (s *knToolsService) boundToolRefs(ctx context.Context, knID, toolboxID string) ([]string, error) {
	// Both entry points come through here, so the per-caller check lives here rather than in each
	// of them: the network's bindings and the execution factory's ranking are both read with this
	// service's identity, and without this the scope would be the kn_id the caller typed.
	if s.knAuthz == nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	if err := s.knAuthz.AuthorizeRead(ctx, knID); err != nil {
		return nil, err
	}

	bindings, err := s.bknBackend.ListKNCapabilities(ctx, knID, "", interfaces.CapabilityTypeFunction)
	if err != nil {
		return nil, err
	}

	refs := make([]string, 0, len(bindings))
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding == nil || binding.CapabilityType != interfaces.CapabilityTypeFunction {
			continue
		}
		boxID := strings.TrimSpace(binding.BoxID)
		toolID := strings.TrimSpace(binding.CapabilityID)
		if boxID == "" || toolID == "" {
			continue
		}
		// toolbox_id narrows within the mounted set; it can never reach outside it.
		if toolboxID != "" && boxID != toolboxID {
			continue
		}
		ref := boxID + "/" + toolID
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	return refs, nil
}

// describeHits fills in the input schema and use rule the ranking does not carry.
//
// It reads the caller-visible catalogue with the caller's own token, which is where the second
// half of the scope comes from: a tool the network mounted but this caller cannot see is absent
// from that catalogue and is dropped here. Listing it would advertise something execute_tool
// would then refuse.
//
// One request per toolbox behind the hits rather than per hit, and only for toolboxes that
// survived ranking — at most `limit` hits, so the fan-out is bounded by the page asked for.
func (s *knToolsService) describeHits(ctx context.Context, hits []interfaces.ToolHit, limit int) []ToolEntry {
	if len(hits) == 0 {
		return nil
	}

	boxOrder := make([]string, 0, len(hits))
	seenBox := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		if hit.BoxID == "" {
			continue
		}
		if _, ok := seenBox[hit.BoxID]; ok {
			continue
		}
		seenBox[hit.BoxID] = struct{}{}
		boxOrder = append(boxOrder, hit.BoxID)
	}

	// toolbox_name stays empty: the caller-visible tools listing does not carry it, and resolving
	// it would mean walking the account's whole toolbox directory — the fan-out this change
	// exists to remove. execute_tool needs the ids, and those are exact.
	catalogue := make([]map[string]interfaces.PublishedToolSummary, len(boxOrder))
	slots := make(chan struct{}, toolboxFanoutConcurrency)
	var wg sync.WaitGroup
	for i, boxID := range boxOrder {
		wg.Add(1)
		go func(i int, boxID string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			listed, err := s.operator.ListPublishedTools(ctx,
				&interfaces.ListPublishedToolsRequest{ToolboxID: boxID})
			if err != nil || listed == nil {
				// One toolbox the caller cannot read must not take down discovery of the rest.
				return
			}
			byID := make(map[string]interfaces.PublishedToolSummary, len(listed.Tools))
			for _, tool := range listed.Tools {
				byID[tool.ToolID] = tool
			}
			catalogue[i] = byID
		}(i, boxID)
	}
	wg.Wait()

	byBox := make(map[string]map[string]interfaces.PublishedToolSummary, len(boxOrder))
	for i, boxID := range boxOrder {
		byBox[boxID] = catalogue[i]
	}

	entries := make([]ToolEntry, 0, len(hits))
	for _, hit := range hits {
		tools := byBox[hit.BoxID]
		if tools == nil {
			continue
		}
		tool, ok := tools[hit.ToolID]
		if !ok {
			continue
		}
		entries = append(entries, ToolEntry{
			ToolID:      hit.ToolID,
			ToolboxID:   hit.BoxID,
			Name:        tool.Name,
			Description: tool.Description,
			UseRule:     tool.UseRule,
			InputSchema: tool.InputSchema,
		})
		if len(entries) >= limit {
			break
		}
	}
	return entries
}

// ExecuteTool invokes one Function tool the knowledge network has mounted.
//
// Two checks, in order: the tool must be mounted on this network, and it must be in the
// caller-visible enabled catalogue. Narrowing discovery is not a control by itself — an id
// outlives the search that produced it — and this is the call that writes.
func (s *knToolsService) ExecuteTool(ctx context.Context, req *ExecuteToolReq) (map[string]any, error) {
	if req == nil || strings.TrimSpace(req.ToolboxID) == "" || strings.TrimSpace(req.ToolID) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "ToolboxIDAndToolIDRequired"))
	}
	if strings.TrimSpace(req.KnID) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "ToolScopeKnIDRequired"))
	}
	toolboxID, toolID := strings.TrimSpace(req.ToolboxID), strings.TrimSpace(req.ToolID)

	refs, err := s.boundToolRefs(ctx, strings.TrimSpace(req.KnID), toolboxID)
	if err != nil {
		return nil, err
	}
	if !containsRef(refs, toolboxID+"/"+toolID) {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "ToolNotMountedOnNetwork"))
	}

	listed, err := s.operator.ListPublishedTools(ctx, &interfaces.ListPublishedToolsRequest{ToolboxID: toolboxID})
	if err != nil {
		return nil, err
	}
	if !containsTool(listed, toolID) {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "ToolNotExecutable"))
	}

	return s.operator.ExecutePublishedTool(ctx, &interfaces.ExecutePublishedToolRequest{
		ToolboxID:  toolboxID,
		ToolID:     toolID,
		Parameters: req.Arguments,
	})
}

// containsRef reports whether the mounted set holds this exact box/tool pair.
func containsRef(refs []string, ref string) bool {
	for _, candidate := range refs {
		if candidate == ref {
			return true
		}
	}
	return false
}

func containsTool(listed *interfaces.ListPublishedToolsResponse, toolID string) bool {
	if listed == nil {
		return false
	}
	for _, tool := range listed.Tools {
		if tool.ToolID == toolID {
			return true
		}
	}
	return false
}
