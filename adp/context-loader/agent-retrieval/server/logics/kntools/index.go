// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kntools exposes the capabilities a knowledge network has mounted to an MCP client:
// search_capabilities finds them, execute_tool runs a Function or MCP tool.
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
	// refillFactor bounds the one wider page asked for after withdrawn owners left a short result
	// (#1443): three pages' worth, and never past maxSearchLimit.
	refillFactor = 3
	// toolboxFanoutConcurrency bounds the catalogue walk. One request per
	// visible toolbox is the shape until a tool dataset exists; keep the burst
	// off Execution Factory small enough that a wide account cannot stall it.
	toolboxFanoutConcurrency = 5
)

// ToolEntry is one callable published Function or MCP tool, as the catalogue describes it.
//
// It is the enrichment describeCapabilityHits produces: what a tool needs to be callable, which a
// Skill does not carry. search_capabilities folds it into CapabilityEntry rather than answering in
// two shapes.
type ToolEntry struct {
	ToolID      string         `json:"tool_id"`
	ToolboxID   string         `json:"toolbox_id"`
	ToolboxName string         `json:"toolbox_name,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	UseRule     string         `json:"use_rule,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
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

// boundToolRefs returns the network's Function bindings as "{box_id}/{tool_id}" references.
//
// A failure to read them fails the search. Continuing with an empty whitelist would look like a
// network that mounted nothing, and continuing without one would return the whole catalogue —
// both answer a question the service cannot currently answer.
//
//nolint:unused // Retained for toolbox binding extensions.
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
//
// Lifecycle comes first and is a different question from visibility (#1443). Whether a box or an
// MCP Server is published is read over the internal face with this service's identity, so it is
// answered on both faces and it fails closed: an owner whose state cannot be confirmed is not
// offered. The index is a catalogue that can lag behind a withdrawal by up to one reconcile, and
// this is the check that keeps a withdrawn tool from being recommended in that window. Visibility
// — may this caller see this tool — keeps the behaviour described above. The count of hits removed
// for lifecycle reasons is returned so the answer can say the result is incomplete rather than
// silently short.
func (s *knToolsService) describeCapabilityHits(ctx context.Context,
	hits []interfaces.CapabilityHit, limit int) ([]ToolEntry, int) {
	if len(hits) == 0 {
		return nil, 0
	}

	hits, dropped := s.dropWithdrawnOwners(ctx, hits)
	if len(hits) == 0 {
		return nil, dropped
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
				s.warnf(ctx, "[SearchCapabilities] published tool catalogue unreadable, box_id=%s: %v", boxID, err)
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
				s.warnf(ctx, "[SearchCapabilities] MCP tool detail unavailable, mcp_id=%s, tool=%s: %v",
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
	return entries, dropped
}

// dropWithdrawnOwners removes every hit whose owner — a tool box or an MCP Server — is not
// currently published, and reports how many it removed.
//
// One status read per distinct owner, not per hit, and only for owners the ranking returned. A
// read that fails counts as unpublished: this decides what is offered for calling, and the safe
// direction when the answer is unknown is to withhold. An MCP Server that is published but does
// not answer its tool listing is a different case and is handled where the listing is read —
// that is runtime health, not lifecycle, and the two must not be confused.
func (s *knToolsService) dropWithdrawnOwners(ctx context.Context,
	hits []interfaces.CapabilityHit) ([]interfaces.CapabilityHit, int) {
	type owner struct{ kind, id string }
	order := make([]owner, 0, len(hits))
	seen := make(map[owner]struct{}, len(hits))
	for _, hit := range hits {
		if hit.OwnerID == "" {
			continue
		}
		switch hit.CapabilityType {
		case interfaces.CapabilityTypeFunction, interfaces.CapabilityTypeMCPTool:
		default:
			continue
		}
		key := owner{hit.CapabilityType, hit.OwnerID}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		order = append(order, key)
	}
	if len(order) == 0 {
		return hits, 0
	}

	// One slot per owner: published or not, and for a tool box which of its tools are enabled.
	// A disabled tool inside a published box is withdrawn too — the caller-visible listing would
	// drop it, but that listing needs a caller token the internal face never has (#1443).
	published := make([]bool, len(order))
	enabled := make([]map[string]struct{}, len(order))
	enabledKnown := make([]bool, len(order))
	slots := make(chan struct{}, toolboxFanoutConcurrency)
	var wg sync.WaitGroup
	for i, key := range order {
		wg.Add(1)
		go func(i int, key owner) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			if key.kind == interfaces.CapabilityTypeMCPTool {
				ok, err := s.operator.MCPServerIsUsable(ctx, key.id)
				if err != nil || !ok {
					s.warnf(ctx, "[SearchCapabilities] owner withheld: mcp server %s not published (err=%v)", key.id, err)
					return
				}
				published[i] = true
				return
			}
			state, err := s.operator.ToolBoxLifecycle(ctx, key.id)
			if err != nil || state == nil || !state.Published {
				s.warnf(ctx, "[SearchCapabilities] owner withheld: tool box %s not published (err=%v)", key.id, err)
				return
			}
			published[i] = true
			enabled[i] = state.EnabledTools
			enabledKnown[i] = state.EnabledKnown
		}(i, key)
	}
	wg.Wait()

	live := make(map[owner]int, len(order))
	for i, key := range order {
		live[key] = i
	}
	kept := make([]interfaces.CapabilityHit, 0, len(hits))
	dropped := 0
	for _, hit := range hits {
		i, gated := live[owner{hit.CapabilityType, hit.OwnerID}]
		if !gated {
			kept = append(kept, hit)
			continue
		}
		if !published[i] {
			dropped++
			continue
		}
		// Enablement is only enforced where it is known. A box too large for the bounded walk
		// reports its enabled set as a prefix; a tool outside it is unknown, and unknown here is
		// kept — the box is confirmed published, and the index itself now admits only enabled
		// tools, so this check is a guard against index lag, not the only gate.
		if hit.CapabilityType == interfaces.CapabilityTypeFunction && enabledKnown[i] {
			if _, on := enabled[i][hit.CapabilityID]; !on {
				s.warnf(ctx, "[SearchCapabilities] tool withheld: %s/%s not enabled", hit.OwnerID, hit.CapabilityID)
				dropped++
				continue
			}
		}
		kept = append(kept, hit)
	}
	return kept, dropped
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
