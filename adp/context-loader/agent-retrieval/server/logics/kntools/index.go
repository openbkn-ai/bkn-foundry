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
	// MetadataTypes optionally restricts Function tools to certain tool box kinds: "openapi" for
	// API tools, "function" for functions. Empty means both, plus MCP tools. It exists because the
	// product presents four kinds where the bindings store three.
	MetadataTypes []string `json:"metadata_types,omitempty"`
	Limit         int      `json:"limit"` // Optional. Caps returned tools, default 20, max 100.
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
	// SearchCapabilities ranks every kind the network mounted against one query (#1388).
	SearchCapabilities(ctx context.Context, req *SearchCapabilitiesReq) (*SearchCapabilitiesResp, error)
	SearchTools(ctx context.Context, req *SearchToolsReq) (*SearchToolsResp, error)
	ExecuteTool(ctx context.Context, req *ExecuteToolReq) (map[string]any, error)
}

type knToolsService struct {
	logger     interfaces.Logger
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
			logger:     conf.GetLogger(),
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

// warnf logs when a logger is configured. A service built without one — tests, and any other
// construction path — must still be able to search; a missing logger is not a reason to panic in
// the middle of answering.
func (s *knToolsService) warnf(ctx context.Context, format string, args ...any) {
	if s.logger == nil {
		return
	}
	s.logger.WithContext(ctx).Warnf(format, args...)
}

// SearchTools returns the Function tools this knowledge network has mounted, ranked against a
// query when one is given.
//
// It is SearchCapabilities with the kinds pinned — literally, by delegation rather than by
// resemblance. The two were copies of each other for exactly one review cycle, long enough for a
// fix to land on one and not the other, which is the argument against keeping two.
//
// What stays here is the shape of the answer: search_tools speaks tool_id / toolbox_id /
// input_schema, and callers, the sandbox SDK and the API contract are written against that.
func (s *knToolsService) SearchTools(ctx context.Context, req *SearchToolsReq) (*SearchToolsResp, error) {
	if req == nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "ToolScopeKnIDRequired"))
	}

	inner, err := s.SearchCapabilities(ctx, &SearchCapabilitiesReq{
		KnID:  req.KnID,
		Query: req.Query,
		// The two tool transports, never Skills: this surface answers with input schemas, and a
		// Skill has none.
		Types:         []string{interfaces.CapabilityTypeFunction, interfaces.CapabilityTypeMCPTool},
		MetadataTypes: req.MetadataTypes,
		Limit:         req.Limit,
		// toolbox_id narrows within the mounted set and can never reach outside it.
		OwnerID: strings.TrimSpace(req.ToolboxID),
	})
	if err != nil {
		return nil, err
	}

	tools := make([]ToolEntry, 0, len(inner.Capabilities))
	for _, c := range inner.Capabilities {
		tools = append(tools, ToolEntry{
			ToolID:      c.CapabilityID,
			ToolboxID:   c.OwnerID,
			Name:        c.Name,
			Description: c.Description,
			UseRule:     c.UseRule,
			InputSchema: c.InputSchema,
		})
	}
	return &SearchToolsResp{
		Tools:        tools,
		TotalMatched: inner.TotalMatched,
		Truncated:    inner.Truncated,
		Message:      inner.Message,
	}, nil
}

// boundToolRefs returns the network's Function bindings as "{box_id}/{tool_id}" references.
//
// A failure to read them fails the search. Continuing with an empty whitelist would look like a
// network that mounted nothing, and continuing without one would return the whole catalogue —
// both answer a question the service cannot currently answer.
func (s *knToolsService) boundToolRefs(ctx context.Context, knID, toolboxID string) ([]string, error) {
	refs, _, err := s.boundRefs(ctx, knID, toolboxID)
	return refs, err
}

// boundRefs returns the network's mounted tools split by transport: toolbox references as
// "{box_id}/{tool_id}", and MCP tools as (mcp_id, tool_name) pairs.
//
// They are read in one call and kept apart because they are called differently — a toolbox tool
// goes through the toolbox proxy, an MCP tool through the MCP proxy — and the ranking endpoint
// only understands the first kind.
func (s *knToolsService) boundRefs(ctx context.Context, knID, toolboxID string) ([]string, []mcpRef, error) {
	// Both entry points come through here, so the per-caller check lives here rather than in each
	// of them: the network's bindings and the execution factory's ranking are both read with this
	// service's identity, and without this the scope would be the kn_id the caller typed.
	if s.knAuthz == nil {
		return nil, nil, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	if err := s.knAuthz.AuthorizeRead(ctx, knID); err != nil {
		return nil, nil, err
	}

	// Empty type: both kinds in one read, rather than one call per capability type.
	bindings, err := s.bknBackend.ListKNCapabilities(ctx, knID, "", "")
	if err != nil {
		return nil, nil, err
	}

	refs := make([]string, 0, len(bindings))
	mcpRefs := make([]mcpRef, 0)
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		if binding.CapabilityType == interfaces.CapabilityTypeMCPTool {
			mcpID := strings.TrimSpace(binding.BoxID)
			toolName := strings.TrimSpace(binding.CapabilityID)
			if mcpID == "" || toolName == "" {
				continue
			}
			// toolbox_id narrows within the mounted set for either transport: an MCP Server id
			// is what owner_id holds for these rows.
			if toolboxID != "" && mcpID != toolboxID {
				continue
			}
			ref := interfaces.CapabilityTypeMCPTool + ":" + mcpID + "/" + toolName
			if _, dup := seen[ref]; dup {
				continue
			}
			seen[ref] = struct{}{}
			mcpRefs = append(mcpRefs, mcpRef{MCPID: mcpID, ToolName: toolName})
			continue
		}
		if binding.CapabilityType != interfaces.CapabilityTypeFunction {
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
	return refs, mcpRefs, nil
}

// mcpRef is one mounted MCP tool: the server that exposes it and its name.
type mcpRef struct {
	MCPID    string
	ToolName string
}

// describeCapabilityHits fills in the input schema and use rule the ranking does not carry, and
// keeps the ranked order across both transports.
//
// The order is the point: the two kinds come back interleaved by one fused rank, and rebuilding
// the answer per kind would put them back into two blocks. Each hit is enriched from the surface
// that owns it — a toolbox tool from the caller-visible tools listing, an MCP tool from the proxy.
//
// That listing is also the second half of the scope, and it is applied where it can be: a tool the
// network mounted but this caller cannot see is absent from it and is dropped, because listing it
// would advertise something execute_tool would then refuse.
//
// Where it cannot be applied at all — the listing is read with the caller's own bearer token, and
// the internal face never carries one — the hit is kept with the name and description the index
// already holds, without an input schema. Dropping it instead is what this used to do, and it
// answered a working query with "no tools matched; publish your tool box first" while the tool box
// was published and the ranking had found five of its tools. The name of a mounted capability is
// not the secret here: the caller is already authorized on the network, the whitelist already
// narrowed to what the network mounted, and execute_tool re-checks the caller-visible catalogue
// before anything runs.
func (s *knToolsService) describeCapabilityHits(ctx context.Context,
	hits []interfaces.CapabilityHit, limit int) []ToolEntry {
	if len(hits) == 0 {
		return nil
	}

	// One request per toolbox behind the hits rather than per hit, and only for toolboxes that
	// survived ranking — at most `limit` hits, so the fan-out is bounded by the page asked for.
	boxOrder := make([]string, 0, len(hits))
	seenBox := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		if hit.CapabilityType != interfaces.CapabilityTypeFunction || hit.OwnerID == "" {
			continue
		}
		if _, ok := seenBox[hit.OwnerID]; ok {
			continue
		}
		seenBox[hit.OwnerID] = struct{}{}
		boxOrder = append(boxOrder, hit.OwnerID)
	}

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
				// Leave this box's slot nil: unreadable, which is not the same as empty. Logged
				// because a silent drop here is indistinguishable from "the box has no tools",
				// and the two want opposite fixes.
				s.warnf(ctx, "[SearchTools] published tool catalogue unreadable, box_id=%s: %v", boxID, err)
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
		if len(entries) >= limit {
			break
		}
		switch hit.CapabilityType {
		case interfaces.CapabilityTypeFunction:
			// A readable box with no tools is a non-nil empty map; nil means the catalogue could
			// not be read at all. The two want opposite answers, so the distinction is the value,
			// not the presence of the key.
			tools := byBox[hit.OwnerID]
			if tools == nil {
				// The catalogue could not be read, so visibility is unknown rather than denied.
				entries = append(entries, ToolEntry{
					ToolID:      hit.CapabilityID,
					ToolboxID:   hit.OwnerID,
					Name:        hit.Name,
					Description: hit.Description,
				})
				continue
			}
			tool, ok := tools[hit.CapabilityID]
			if !ok {
				// The catalogue was read and this tool is not in it: the caller cannot see it.
				continue
			}
			// toolbox_name stays empty: the caller-visible tools listing does not carry it, and
			// resolving it would mean walking the account's whole toolbox directory — the fan-out
			// this design exists to remove. execute_tool needs the ids, and those are exact.
			entries = append(entries, ToolEntry{
				ToolID:      hit.CapabilityID,
				ToolboxID:   hit.OwnerID,
				Name:        tool.Name,
				Description: tool.Description,
				UseRule:     tool.UseRule,
				InputSchema: tool.InputSchema,
			})
		case interfaces.CapabilityTypeMCPTool:
			detail, err := s.operator.GetMCPToolDetail(ctx, &interfaces.GetMCPToolDetailRequest{
				McpID: hit.OwnerID, ToolName: hit.CapabilityID,
			})
			if err != nil || detail == nil {
				// An unreachable MCP Server is not an empty one. Keep what the index knows so a
				// server that is briefly down does not make a network's mounted tools vanish from
				// search; the input schema is simply absent until it answers again.
				s.warnf(ctx, "[SearchTools] MCP tool detail unavailable, mcp_id=%s, tool=%s: %v",
					hit.OwnerID, hit.CapabilityID, err)
				entries = append(entries, ToolEntry{
					ToolID:      hit.CapabilityID,
					ToolboxID:   hit.OwnerID,
					Name:        hit.Name,
					Description: hit.Description,
				})
				continue
			}
			entries = append(entries, ToolEntry{
				ToolID:      hit.CapabilityID,
				ToolboxID:   hit.OwnerID,
				Name:        detail.Name,
				Description: detail.Description,
				InputSchema: detail.InputSchema,
			})
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

	refs, mcpRefs, err := s.boundRefs(ctx, strings.TrimSpace(req.KnID), toolboxID)
	if err != nil {
		return nil, err
	}

	// An MCP tool runs through the MCP proxy, not the toolbox proxy. Which one a binding meant is
	// decided here, by the mount that authorized it, rather than guessed from the ids — the two
	// id spaces do not overlap in any way a caller could rely on.
	for _, ref := range mcpRefs {
		if ref.MCPID != toolboxID || ref.ToolName != toolID {
			continue
		}
		// The mount says this network may use the tool; it does not say the tool still works.
		// A server published at mount time can be taken offline afterwards — bkn-backend then
		// refuses new bindings, but the existing one survives, and the MCP proxy performs no
		// status check of its own, so without this the call would still run.
		//
		// The server is asked directly. Its tool listing is not a proxy for this question: that
		// endpoint answers whatever the server's state, so an offline server still lists every
		// tool it had.
		usable, err := s.operator.MCPServerIsUsable(ctx, ref.MCPID)
		if err != nil || !usable {
			return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
				infraErr.LocalizedDetail(ctx, "ToolNotExecutable"))
		}
		return s.operator.CallMCPTool(ctx, &interfaces.CallMCPToolRequest{
			McpID:      ref.MCPID,
			ToolName:   ref.ToolName,
			Parameters: req.Arguments,
		})
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
