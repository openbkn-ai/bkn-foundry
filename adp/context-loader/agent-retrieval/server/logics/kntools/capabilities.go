// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kntools

import (
	"context"
	"net/http"
	"strings"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// search_capabilities asks one question over everything a knowledge network mounted: what can do
// this? (#1388)
//
// find_skills and search_tools each answered for their own kind, so an agent asking about
// currency conversion through search_tools never saw the Skill that did it. The index and the
// ranking have been shared since #1370 — the split survived only in the entry points, and it put
// the burden of merging two incomparable answers back on the agent.
//
// Both were this call with types pinned, by delegation rather than resemblance, and were removed
// once their callers moved (#1401).

// SearchCapabilitiesReq is the input for search_capabilities.
type SearchCapabilitiesReq struct {
	// KnID is required. Without it there is no scope to narrow to, and answering anyway would
	// return every capability the account can see.
	KnID  string `json:"kn_id"`
	Query string `json:"query"` // Optional. Ranks the mounted capabilities by name and description.
	// Types narrows to certain capability kinds: "skill", "function", "mcp_tool". Empty means all
	// three. It narrows within what the network mounted and can never reach outside it.
	Types []string `json:"types,omitempty"`
	// MetadataTypes narrows Function tools to a tool box kind: "openapi" for API tools, "function"
	// for functions. Empty means both.
	MetadataTypes []string `json:"metadata_types,omitempty"`
	// OwnerID narrows to one owner: a tool box for Function tools, a server for MCP tools. Like
	// every other filter here it narrows within the mounted set and can never reach outside it.
	OwnerID string `json:"owner_id,omitempty"`
	Limit   int    `json:"limit"` // Optional. Caps returned capabilities, default 20, max 100.
}

// CapabilityEntry is one mounted capability, whatever kind it is.
//
// CapabilityType is what tells an agent how to use it: a tool is called through execute_tool with
// arguments matching InputSchema, a Skill is read through get_skill_content and run through
// execute_skill. Returning them in one list without saying which is which would be worse than two
// lists.
type CapabilityEntry struct {
	CapabilityType string `json:"capability_type"`
	// OwnerID is the tool box for a Function tool, the server for an MCP tool, empty for a Skill.
	OwnerID      string `json:"owner_id,omitempty"`
	CapabilityID string `json:"capability_id"`
	// MetadataType is the tool box kind for a Function tool: "openapi" or "function".
	MetadataType string `json:"metadata_type,omitempty"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	// UseRule and InputSchema are filled for tools only. A Skill carries neither: it is read, not
	// called with arguments.
	UseRule     string         `json:"use_rule,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// SearchCapabilitiesResp is the search_capabilities result.
type SearchCapabilitiesResp struct {
	Capabilities []CapabilityEntry `json:"capabilities"`
	TotalMatched int               `json:"total_matched"`
	Truncated    bool              `json:"truncated,omitempty"`
	Message      string            `json:"message,omitempty"`
}

// SearchCapabilities ranks every kind the network mounted against one query.
func (s *knToolsService) SearchCapabilities(ctx context.Context,
	req *SearchCapabilitiesReq) (*SearchCapabilitiesResp, error) {
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

	// The kinds are applied to the whitelist here as well as sent downstream. Sending them alone
	// would make the scope depend on the ranking honouring a filter, which is not a scope at all:
	// a kind the caller excluded would come back on any hit the ranking let through.
	searchRefs, err := s.allBoundRefs(ctx, strings.TrimSpace(req.KnID),
		strings.TrimSpace(req.OwnerID), normalizeKinds(req.Types))
	if err != nil {
		return nil, err
	}
	if len(searchRefs) == 0 {
		return &SearchCapabilitiesResp{
			Capabilities: []CapabilityEntry{},
			Message:      infraErr.LocalizedDetail(ctx, "NoBoundCapabilitiesInNetwork"),
		}, nil
	}

	// One more than the page, purely to learn whether there is a next one. The ranking caps its
	// answer at top_k, so asking for exactly `limit` makes a full page and a truncated page look
	// identical — the truncation flag could never fire, and a caller would read one page as the
	// whole answer.
	query := strings.TrimSpace(req.Query)
	// metadata_type lives only in the index document — a binding carries the capability type, not
	// the tool box kind behind it. So this is the one filter that cannot be answered without the
	// ranking, and a listing that quietly skipped it would return precisely what the caller
	// excluded. Everything else (kinds, owner) was already applied to the whitelist above.
	metadataTypes := normalizeKinds(req.MetadataTypes)
	listing := query == "" && len(metadataTypes) == 0

	// A listing pages the mounted set itself and then asks the ranking to describe exactly that
	// page. Asking the ranking for a page instead returns the top `limit` for an empty query,
	// which is a different subset from the first `limit` bindings — the page would come back
	// mostly undescribed, and how much of it was described would depend on `limit`.
	askRefs, askTopK := searchRefs, limit+1
	if listing {
		if len(askRefs) > limit+1 {
			askRefs = askRefs[:limit+1]
		}
		askTopK = len(askRefs)
	}

	hits, err := s.operator.SearchCapabilities(ctx, &interfaces.SearchCapabilitiesRequest{
		Query:         query,
		Refs:          askRefs,
		TopK:          askTopK,
		Types:         normalizeKinds(req.Types),
		MetadataTypes: metadataTypes,
	})
	if err != nil {
		// A ranking has nothing to degrade to, and neither has a filter only the index can
		// evaluate. Both surface the error rather than answering a different question.
		if !listing {
			return nil, err
		}
		s.warnf(ctx, "[SearchCapabilities] capability search failed, listing from the bindings: %v", err)
		hits = nil
	}
	if listing {
		hits = s.listingHits(ctx, askRefs, hits, limit+1)
	}

	more := len(hits) > limit
	if more {
		hits = hits[:limit]
	}

	entries := s.describeCapabilities(ctx, hits, limit)
	total := len(hits)
	resp := &SearchCapabilitiesResp{Capabilities: entries, TotalMatched: total}

	fitted := total
	if fitted > limit {
		fitted = limit
	}
	switch {
	case len(entries) == 0 && total > 0:
		resp.Message = infraErr.LocalizedDetail(ctx, "ToolsMatchedButNotVisible")
	// Both filters are the caller's now that no entry point pins kinds of its own, so the message
	// may name either without telling anyone to drop a parameter they cannot set.
	case len(entries) == 0 && len(req.Types) > 0,
		len(entries) == 0 && len(req.MetadataTypes) > 0:
		resp.Message = infraErr.LocalizedDetail(ctx, "NoCapabilitiesOfRequestedKind")
	case len(entries) == 0:
		resp.Message = infraErr.LocalizedDetail(ctx, "NoPublishedToolsMatched")
	case more:
		resp.Truncated = true
		resp.Message = infraErr.LocalizedDetail(ctx, "ToolSearchTruncated")
	case len(entries) < fitted:
		resp.Message = infraErr.LocalizedDetail(ctx, "ToolsMatchedButNotVisible")
	}
	if len(resp.Capabilities) == 0 {
		resp.Capabilities = []CapabilityEntry{}
	}
	return resp, nil
}

// allBoundRefs reads every capability the network mounted, of all three kinds.
//
// boundRefs splits Function and MCP tools apart because they are called through different proxies.
// Here they are not called, only ranked, so the split would be noise — and Skills, which boundRefs
// drops entirely, belong in the answer.
func (s *knToolsService) allBoundRefs(ctx context.Context,
	knID, ownerID string, kinds []string) ([]interfaces.SearchCapabilityRef, error) {
	// The per-caller check lives here for the same reason it lives in boundRefs: the bindings and
	// the ranking are both read with this service's identity, and without it the scope would be
	// the kn_id the caller typed.
	if s.knAuthz == nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	if err := s.knAuthz.AuthorizeRead(ctx, knID); err != nil {
		return nil, err
	}

	// A failure to read the bindings fails the call. Continuing with an empty whitelist would look
	// like a network that mounted nothing, and continuing without one would return the whole
	// platform — both answer a question this service cannot currently answer.
	bindings, err := s.bknBackend.ListKNCapabilities(ctx, knID, "", "")
	if err != nil {
		return nil, err
	}

	wanted := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		wanted[kind] = struct{}{}
	}

	refs := make([]interfaces.SearchCapabilityRef, 0, len(bindings))
	seen := make(map[interfaces.SearchCapabilityRef]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		id := strings.TrimSpace(binding.CapabilityID)
		if id == "" {
			continue
		}
		owner := strings.TrimSpace(binding.BoxID)
		switch binding.CapabilityType {
		case interfaces.CapabilityTypeSkill:
			// A Skill has no owner; a binding that carries one is still a Skill.
			owner = ""
		case interfaces.CapabilityTypeFunction, interfaces.CapabilityTypeMCPTool:
			if owner == "" {
				continue
			}
		default:
			continue
		}
		// Narrowing happens here rather than only in the ranking, so the whitelist that leaves
		// this service already is the scope: nothing downstream can widen it back.
		if len(wanted) > 0 {
			if _, ok := wanted[binding.CapabilityType]; !ok {
				continue
			}
		}
		if ownerID != "" && owner != ownerID {
			continue
		}
		ref := interfaces.SearchCapabilityRef{
			CapabilityType: binding.CapabilityType,
			OwnerID:        owner,
			CapabilityID:   id,
		}
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	return refs, nil
}

// describeCapabilities enriches the ranked hits in place, keeping the fused order.
//
// A tool needs its input schema to be callable; a Skill needs nothing, because it is read rather
// than called. Rebuilding the answer per kind would put the two back into two blocks and undo the
// one ranking this endpoint exists to deliver.
func (s *knToolsService) describeCapabilities(ctx context.Context,
	hits []interfaces.CapabilityHit, limit int) []CapabilityEntry {
	if len(hits) == 0 {
		return nil
	}

	toolHits := make([]interfaces.CapabilityHit, 0, len(hits))
	for _, hit := range hits {
		if hit.CapabilityType != interfaces.CapabilityTypeSkill {
			toolHits = append(toolHits, hit)
		}
	}
	// describeCapabilityHits owns the catalogue fan-out and the visibility rule; reusing it keeps
	// one answer to "may this caller see this tool" instead of two.
	described := s.describeCapabilityHits(ctx, toolHits, len(toolHits))
	byRef := make(map[string]ToolEntry, len(described))
	for _, entry := range described {
		byRef[entry.ToolboxID+"/"+entry.ToolID] = entry
	}

	entries := make([]CapabilityEntry, 0, len(hits))
	for _, hit := range hits {
		if len(entries) >= limit {
			break
		}
		entry := CapabilityEntry{
			CapabilityType: hit.CapabilityType,
			OwnerID:        hit.OwnerID,
			CapabilityID:   hit.CapabilityID,
			MetadataType:   hit.MetadataType,
			Name:           hit.Name,
			Description:    hit.Description,
		}
		if hit.CapabilityType == interfaces.CapabilityTypeSkill {
			entries = append(entries, entry)
			continue
		}
		described, ok := byRef[hit.OwnerID+"/"+hit.CapabilityID]
		if !ok {
			// The tool was dropped by the visibility rule. Skipping it keeps this endpoint's answer
			// to what execute_tool will accept: listing it would advertise a call that gets
			// refused.
			continue
		}
		if described.Name != "" {
			entry.Name = described.Name
			entry.Description = described.Description
		}
		entry.UseRule = described.UseRule
		entry.InputSchema = described.InputSchema
		entries = append(entries, entry)
	}
	return entries
}

// listingHits answers a listing from the mounted set, in binding order, using the index only for
// what it can add.
//
// With no query there is nothing to rank against, so membership is the bindings' to decide, not
// the index's. This is what find_skills did and what search_capabilities has to keep now that it
// is the only entry: the index is built asynchronously and a fresh install has none (#1323), so
// letting it decide membership makes a capability that is mounted but not yet indexed invisible —
// a wrong answer, where binding order is merely an unranked one.
//
// Index hits still carry the name, description and metadata_type, so they are merged in wherever
// the index knew the capability. Tools the index missed are named by describeCapabilities from the
// catalogue; Skills are named here, since nothing downstream looks them up.
//
// A Skill neither the index nor the registry knows is dropped rather than listed. The binding
// outlives deletion in the execution factory, so listing it would send the caller after something
// that cannot run.
func (s *knToolsService) listingHits(ctx context.Context, refs []interfaces.SearchCapabilityRef,
	ranked []interfaces.CapabilityHit, limit int) []interfaces.CapabilityHit {
	if len(refs) > limit {
		refs = refs[:limit]
	}
	known := make(map[interfaces.SearchCapabilityRef]interfaces.CapabilityHit, len(ranked))
	for _, hit := range ranked {
		known[hit.SearchCapabilityRef] = hit
	}

	missingSkills := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.CapabilityType != interfaces.CapabilityTypeSkill {
			continue
		}
		if _, ok := known[ref]; !ok {
			missingSkills = append(missingSkills, ref.CapabilityID)
		}
	}
	var names map[string]string
	if len(missingSkills) > 0 {
		resolved, err := s.operator.GetSkillNamesByIDs(ctx, missingSkills)
		if err != nil {
			// The names are decoration; the memberships are not. Without them a Skill the index
			// has not reached cannot be told apart from a dead binding, so those are dropped and
			// everything else still answers.
			s.warnf(ctx, "[SearchCapabilities] skill name lookup failed for %d skills: %v",
				len(missingSkills), err)
		} else {
			names = resolved
		}
	}

	hits := make([]interfaces.CapabilityHit, 0, len(refs))
	for _, ref := range refs {
		if hit, ok := known[ref]; ok {
			hits = append(hits, hit)
			continue
		}
		hit := interfaces.CapabilityHit{SearchCapabilityRef: ref}
		if ref.CapabilityType == interfaces.CapabilityTypeSkill {
			name, ok := names[ref.CapabilityID]
			if !ok {
				s.warnf(ctx, "[SearchCapabilities] bound skill %s is unknown to the execution factory",
					ref.CapabilityID)
				continue
			}
			hit.Name = name
		}
		hits = append(hits, hit)
	}
	return hits
}

// normalizeKinds trims, drops blanks and de-duplicates.
func normalizeKinds(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
