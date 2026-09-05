package toolbox

import (
	"context"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

const (
	defaultToolSearchTopK = 10
	maxToolSearchTopK     = 100
	// maxToolSearchWhitelist bounds one request. Past this the caller should resolve the
	// whitelist server-side rather than post a list nobody can read in a log.
	maxToolSearchWhitelist = 1000
)

// toolRef is the (box, tool) pair a flat "{box_id}/{tool_id}" reference denotes.
type toolRef struct {
	boxID  string
	toolID string
}

// parseToolRefs normalises the whitelist. Malformed entries are dropped rather than rejected: the
// list is assembled from stored bindings, and one stale entry should narrow the search, not fail
// the request.
func parseToolRefs(refs []string) []toolRef {
	seen := make(map[toolRef]struct{}, len(refs))
	parsed := make([]toolRef, 0, len(refs))
	for _, raw := range refs {
		boxID, toolID, ok := strings.Cut(strings.TrimSpace(raw), "/")
		boxID, toolID = strings.TrimSpace(boxID), strings.TrimSpace(toolID)
		if !ok || boxID == "" || toolID == "" {
			continue
		}
		ref := toolRef{boxID: boxID, toolID: toolID}
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		parsed = append(parsed, ref)
	}
	return parsed
}

// SearchTools retrieves tools from the whitelist by literal substring containment.
//
// There is no tool index yet — the execution factory has never built one, and the whole "search"
// surface on tools is SQL matching — so retrieval is a LIKE over name and description, reported
// honestly as matched_by=like. The response shape is the semantic one so that replacing the
// implementation later does not change the contract.
func (s *ToolServiceImpl) SearchTools(ctx context.Context,
	req *interfaces.SearchToolsReq) (*interfaces.SearchToolsResp, error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	var err error
	defer func() { oteltrace.EndSpan(ctx, err) }()

	empty := &interfaces.SearchToolsResp{Entries: []*interfaces.SearchToolHit{}}

	// Fail closed. An empty or missing whitelist means "no tool is in scope", never "do not
	// filter" — the second reading would hand every tool on the platform to a caller that has
	// bound none.
	refs := parseToolRefs(req.ToolRefs)
	if len(refs) == 0 {
		s.Logger.WithContext(ctx).Infof("tool search short-circuited: empty whitelist")
		return empty, nil
	}
	if len(refs) > maxToolSearchWhitelist {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest,
			"tool_refs exceeds the maximum of 1000 entries")
		return nil, err
	}

	topK := req.TopK
	if topK <= 0 {
		topK = defaultToolSearchTopK
	}
	if topK > maxToolSearchTopK {
		topK = maxToolSearchTopK
	}

	allowed := make(map[toolRef]struct{}, len(refs))
	toolIDs := make([]string, 0, len(refs))
	for _, ref := range refs {
		allowed[ref] = struct{}{}
		toolIDs = append(toolIDs, ref.toolID)
	}

	// The database filter is on tool_id alone because a tool id is scoped to its box, so the
	// same id can exist in another box. The rows are narrowed to the exact pairs below; without
	// that step a caller could reach a tool of the same id in a box it never bound.
	// No SQL limit either: the cap applies after pair filtering, or a page full of same-id tools
	// from other boxes would crowd out the caller's own.
	tools, err := s.ToolDB.SearchToolsByIDs(ctx, toolIDs, strings.TrimSpace(req.Query), 0)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("tool search query failed: %v", err)
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}

	entries := make([]*interfaces.SearchToolHit, 0, len(tools))
	for _, tool := range tools {
		if tool == nil || tool.IsDeleted {
			continue
		}
		if _, ok := allowed[toolRef{boxID: tool.BoxID, toolID: tool.ToolID}]; !ok {
			continue
		}
		entries = append(entries, &interfaces.SearchToolHit{
			BoxID:       tool.BoxID,
			ToolID:      tool.ToolID,
			Name:        tool.Name,
			Description: tool.Description,
			Status:      tool.Status,
			MatchedBy:   interfaces.ToolMatchedByLike,
		})
		if len(entries) >= topK {
			break
		}
	}
	return &interfaces.SearchToolsResp{Entries: entries}, nil
}
