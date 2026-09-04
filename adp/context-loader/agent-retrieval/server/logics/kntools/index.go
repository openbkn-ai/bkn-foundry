// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kntools exposes the published Function tools registered in Execution
// Factory to an MCP client: search_tools finds them, execute_tool runs one.
//
// Discovery is a listing today, not a ranked search. Execution Factory has no
// cross-toolbox tool enumeration endpoint and no tool dataset to embed against,
// so the search argument filters a caller-visible catalogue this layer walks.
// The contract is written for what replaces it (#1009): callers pass a natural
// language query and read back a bounded list, which stays true once ranking
// moves behind a dataset.
package kntools

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
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
	Query     string `json:"query"`      // Optional. Filters by tool name, description, use rule, and toolbox name.
	ToolboxID string `json:"toolbox_id"` // Optional. Restricts the search to one published toolbox.
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
	operator interfaces.DrivenOperatorIntegration
}

var (
	once    sync.Once
	service KnToolsService
)

// NewKnToolsService creates the KnToolsService singleton.
func NewKnToolsService() KnToolsService {
	once.Do(func() {
		service = &knToolsService{operator: drivenadapters.NewOperatorIntegrationClient()}
	})
	return service
}

// NewKnToolsServiceWith builds a service over an explicit driven adapter.
func NewKnToolsServiceWith(operator interfaces.DrivenOperatorIntegration) KnToolsService {
	return &knToolsService{operator: operator}
}

// SearchTools returns the callable published Function tools matching a query.
func (s *knToolsService) SearchTools(ctx context.Context, req *SearchToolsReq) (*SearchToolsResp, error) {
	if req == nil {
		req = &SearchToolsReq{}
	}
	limit := req.Limit
	if limit < 1 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	toolboxes, err := s.visibleToolboxes(ctx, strings.TrimSpace(req.ToolboxID))
	if err != nil {
		return nil, err
	}

	matched := s.collectTools(ctx, toolboxes, strings.TrimSpace(req.Query))
	resp := &SearchToolsResp{TotalMatched: len(matched)}
	if len(matched) > limit {
		resp.Tools = matched[:limit]
		resp.Truncated = true
		resp.Message = infraErr.LocalizedDetail(ctx, "ToolSearchTruncated")
	} else {
		resp.Tools = matched
	}
	if len(resp.Tools) == 0 {
		resp.Tools = []ToolEntry{}
		resp.Message = infraErr.LocalizedDetail(ctx, "NoPublishedToolsMatched")
	}
	return resp, nil
}

// visibleToolboxes resolves the toolboxes to walk. An explicit toolbox_id is
// taken at face value: the catalogue call below authorizes it as the caller,
// and looking it up in the directory first would cost a request to learn a name.
func (s *knToolsService) visibleToolboxes(
	ctx context.Context, toolboxID string,
) ([]interfaces.PublishedToolboxSummary, error) {
	if toolboxID != "" {
		return []interfaces.PublishedToolboxSummary{{ToolboxID: toolboxID}}, nil
	}
	// The query is deliberately not forwarded as the toolbox name filter: a
	// matching tool inside a differently named toolbox must still be found.
	catalogue, err := s.operator.ListPublishedToolboxes(ctx, &interfaces.ListPublishedToolboxesRequest{})
	if err != nil {
		return nil, err
	}
	if catalogue == nil {
		return nil, nil
	}
	return catalogue.Toolboxes, nil
}

// collectTools walks the toolboxes and keeps the enabled tools that match.
//
// A toolbox that fails is skipped rather than failing the search: one revoked
// or broken toolbox in a wide account would otherwise take down discovery of
// every other tool the caller can reach.
func (s *knToolsService) collectTools(
	ctx context.Context, toolboxes []interfaces.PublishedToolboxSummary, query string,
) []ToolEntry {
	if len(toolboxes) == 0 {
		return nil
	}
	perToolbox := make([][]ToolEntry, len(toolboxes))
	slots := make(chan struct{}, toolboxFanoutConcurrency)
	var wg sync.WaitGroup
	for i, box := range toolboxes {
		wg.Add(1)
		go func(i int, box interfaces.PublishedToolboxSummary) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			listed, err := s.operator.ListPublishedTools(ctx,
				&interfaces.ListPublishedToolsRequest{ToolboxID: box.ToolboxID})
			if err != nil || listed == nil {
				return
			}
			entries := make([]ToolEntry, 0, len(listed.Tools))
			for _, tool := range listed.Tools {
				entry := ToolEntry{
					ToolID:      tool.ToolID,
					ToolboxID:   box.ToolboxID,
					ToolboxName: box.Name,
					Name:        tool.Name,
					Description: tool.Description,
					UseRule:     tool.UseRule,
					InputSchema: tool.InputSchema,
				}
				if !matchesQuery(entry, query) {
					continue
				}
				entries = append(entries, entry)
			}
			perToolbox[i] = entries
		}(i, box)
	}
	wg.Wait()

	// Ordered by toolbox position, so the same catalogue answers the same way twice.
	matched := make([]ToolEntry, 0, len(toolboxes))
	for _, entries := range perToolbox {
		matched = append(matched, entries...)
	}
	return matched
}

// matchesQuery is literal substring matching, case-insensitive. It is the
// interim stand-in for semantic recall: it cannot answer "a tool that converts
// currency" when the tool is named fx_convert, which is exactly why the tool
// dataset in #1009 replaces it.
func matchesQuery(entry ToolEntry, query string) bool {
	if query == "" {
		return true
	}
	needle := strings.ToLower(query)
	for _, field := range []string{entry.Name, entry.Description, entry.UseRule, entry.ToolboxName} {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
}

// ExecuteTool invokes one published Function tool.
//
// The tool is confirmed against the caller-visible enabled catalogue first. A
// disabled or unknown tool then fails as a 400 that names the reason, instead of
// whatever the proxy returns for an id it cannot resolve.
func (s *knToolsService) ExecuteTool(ctx context.Context, req *ExecuteToolReq) (map[string]any, error) {
	if req == nil || strings.TrimSpace(req.ToolboxID) == "" || strings.TrimSpace(req.ToolID) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "ToolboxIDAndToolIDRequired"))
	}
	toolboxID, toolID := strings.TrimSpace(req.ToolboxID), strings.TrimSpace(req.ToolID)

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
