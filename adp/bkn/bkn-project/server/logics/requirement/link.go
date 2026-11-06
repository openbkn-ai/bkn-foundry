// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
)

// A kinetic need is implemented in the project's live network, by whoever builds metrics, functions,
// Skills and action types there. The project links a need to that implementation and reads it live,
// as the caller, so what the project shows is always what the network has: nothing of it is copied.

// maxCandidates bounds the candidates offered for a need.
const maxCandidates = 20

// liveReader reads a live network's implementations once each per request, as the caller.
type liveReader struct {
	s            *requirementService
	ctx          context.Context
	token        string
	network      string
	capabilities map[string][]*interfaces.KNCapability
	actionTypes  []*interfaces.KNActionType
	errs         map[string]error
}

func (s *requirementService) liveReader(ctx context.Context, token string, network string) *liveReader {
	return &liveReader{s: s, ctx: ctx, token: token, network: network,
		capabilities: map[string][]*interfaces.KNCapability{}, errs: map[string]error{}}
}

func (r *liveReader) listCapabilities(capabilityType string, metadataType string) ([]*interfaces.KNCapability, error) {
	key := capabilityType + "/" + metadataType
	if err, ok := r.errs[key]; ok {
		return nil, err
	}
	if listed, ok := r.capabilities[key]; ok {
		return listed, nil
	}
	listed, err := r.s.bba.ListCapabilities(r.ctx, r.token, r.network, capabilityType, metadataType)
	if err != nil {
		r.errs[key] = err
		return nil, err
	}
	r.capabilities[key] = listed
	return listed, nil
}

func (r *liveReader) listActionTypes() ([]*interfaces.KNActionType, error) {
	if err, ok := r.errs["action_types"]; ok {
		return nil, err
	}
	if r.actionTypes == nil {
		listed, err := r.s.bba.ListActionTypes(r.ctx, r.token, r.network, "")
		if err != nil {
			r.errs["action_types"] = err
			return nil, err
		}
		r.actionTypes = listed
	}
	return r.actionTypes, nil
}

// unreadable is why an implementation could not be read: bkn-backend's 404 with a code is a thing
// the network does not have, its plain 404 a feature it does not serve, as metrics without the
// professional edition; 403 is the caller's lack of rights.
func unreadable(err error) *interfaces.LogicNeedImplementation {
	impl := &interfaces.LogicNeedImplementation{Reason: interfaces.LOGIC_IMPLEMENTATION_UNAVAILABLE}
	var backendErr *interfaces.BknBackendError
	if errors.As(err, &backendErr) {
		switch {
		case backendErr.HTTPCode == http.StatusForbidden:
			impl.Reason = interfaces.LOGIC_IMPLEMENTATION_FORBIDDEN
		case backendErr.HTTPCode == http.StatusNotFound && backendErr.ErrorCode != "":
			impl.Reason = interfaces.LOGIC_IMPLEMENTATION_MISSING
		}
	}
	return impl
}

// read is the linked implementation of a need of kind as the live network has it now. An API is a
// function bound from an OpenAPI description; a Skill is named by its id alone.
func (r *liveReader) read(link *interfaces.RequirementLogicLink, kind string) *interfaces.LogicNeedImplementation {
	missing := &interfaces.LogicNeedImplementation{Reason: interfaces.LOGIC_IMPLEMENTATION_MISSING}
	switch link.Type {
	case interfaces.LOGIC_LINK_OBJECT_METRIC:
		m, err := r.s.bba.GetObjectMetric(r.ctx, r.token, r.network, link.ID)
		if err != nil {
			return unreadable(err)
		}
		return &interfaces.LogicNeedImplementation{Available: true, Name: m.Name, Description: m.Description,
			Status: m.Status, Version: m.Version, OwnerObjectTypeID: m.OwnerObjectTypeID, OwnerObjectTypeName: m.OwnerObjectTypeName}
	case interfaces.LOGIC_LINK_FUNCTION, interfaces.LOGIC_LINK_SKILL, interfaces.LOGIC_LINK_MCP_TOOL:
		listed, err := r.listCapabilities(link.Type, capabilityMetadataType(kind))
		if err != nil {
			return unreadable(err)
		}
		for _, c := range listed {
			if c.CapabilityID == link.ID && (link.Type == interfaces.LOGIC_LINK_SKILL || c.BoxID == link.BoxID) {
				return &interfaces.LogicNeedImplementation{Available: true, Name: c.Name, Description: c.Description, Status: c.Status}
			}
		}
		return missing
	case interfaces.LOGIC_LINK_ACTION_TYPE:
		listed, err := r.listActionTypes()
		if err != nil {
			return unreadable(err)
		}
		for _, a := range listed {
			if a.ID == link.ID {
				return &interfaces.LogicNeedImplementation{Available: true, Name: a.Name, OwnerObjectTypeID: a.ObjectTypeID}
			}
		}
		return missing
	}
	return missing
}

// capabilityMetadataType narrows the functions an API need is implemented by to those bound from an
// OpenAPI description.
func capabilityMetadataType(kind string) string {
	if kind == interfaces.LOGIC_NEED_API {
		return "openapi"
	}
	return ""
}

// liveNetwork is the project's live network, and the object types it has; reason says why they
// could not be read.
func (s *requirementService) liveNetwork(ctx context.Context, token string, entry *interfaces.ProjectEntry) (string, map[string]bool, string, error) {
	network, err := logics.LiveNetwork(ctx, s.rla, entry.KNID)
	if err != nil {
		return "", nil, "", err
	}
	ids, err := s.bba.ListObjectTypeIDs(ctx, token, network)
	if err != nil {
		return network, map[string]bool{}, interfaces.LOGIC_TARGET_UNAVAILABLE, nil
	}
	live := map[string]bool{}
	for _, id := range ids {
		live[id] = true
	}
	return network, live, "", nil
}

func (s *requirementService) ListLogicNeeds(ctx context.Context, token string, projectID string) (*interfaces.LogicNeedsView, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListLogicNeeds")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	req, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	if !ok {
		span.SetStatus(codes.Error, "Requirement not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)
	}
	network, live, reason, err := s.liveNetwork(ctx, token, entry)
	if err != nil {
		span.SetStatus(codes.Error, "Get live network failed")
		return nil, internalError(ctx, "Get live network failed", err)
	}
	view := &interfaces.LogicNeedsView{UpdateTime: req.UpdateTime, NetworkID: network, Reason: reason,
		Needs: []*interfaces.LogicNeedView{}}
	reader := s.liveReader(ctx, token, network)
	for _, n := range req.Spec.LogicNeeds {
		v := &interfaces.LogicNeedView{RequirementLogicNeed: n, LiveObjectTypeIDs: []string{}}
		for _, id := range n.ObjectTypeIDs {
			if live[id] {
				v.LiveObjectTypeIDs = append(v.LiveObjectTypeIDs, id)
			}
		}
		// A link of another kind than the need's, from before its kind was changed, does not hold.
		if n.Link != nil && n.Link.Type == interfaces.LogicLinkTypeOf(n.Kind) {
			v.Implementation = reader.read(n.Link, n.Kind)
		}
		view.Needs = append(view.Needs, v)
	}
	span.SetStatus(codes.Ok, "")
	return view, nil
}

// squeezed is a name compared without case, spaces, underscores or hyphens.
func squeezed(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '_' || r == '-' {
			return -1
		}
		return r
	}, strings.ToLower(strings.TrimSpace(name)))
}

// candidateReasons says why an implementation of name, on owner, may be the need's.
func candidateReasons(need *interfaces.RequirementLogicNeed, name string, owner string) []string {
	reasons := []string{}
	a, b := squeezed(need.Name), squeezed(name)
	switch {
	case a == "" || b == "":
	case a == b:
		reasons = append(reasons, interfaces.LOGIC_CANDIDATE_SAME_NAME)
	case strings.Contains(a, b) || strings.Contains(b, a):
		reasons = append(reasons, interfaces.LOGIC_CANDIDATE_SIMILAR)
	}
	if owner != "" && slices.Contains(need.ObjectTypeIDs, owner) {
		reasons = append(reasons, interfaces.LOGIC_CANDIDATE_SAME_OBJECT)
	}
	return reasons
}

func candidateRank(c *interfaces.LogicNeedCandidate) int {
	rank := 0
	for _, r := range c.Reasons {
		switch r {
		case interfaces.LOGIC_CANDIDATE_SAME_NAME:
			rank += 4
		case interfaces.LOGIC_CANDIDATE_SIMILAR:
			rank += 2
		case interfaces.LOGIC_CANDIDATE_SAME_OBJECT:
			rank++
		}
	}
	return rank
}

func (s *requirementService) LogicNeedCandidates(ctx context.Context, token string, projectID string,
	needID string) (*interfaces.LogicNeedCandidates, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "LogicNeedCandidates")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	req, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	if !ok {
		span.SetStatus(codes.Error, "Requirement not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)
	}
	i, found := find(req.Spec.LogicNeeds, needID, logicNeedID)
	if !found {
		span.SetStatus(codes.Error, "Need not found")
		return nil, notFound(ctx)
	}
	need := req.Spec.LogicNeeds[i]
	network, err := logics.LiveNetwork(ctx, s.rla, entry.KNID)
	if err != nil {
		span.SetStatus(codes.Error, "Get live network failed")
		return nil, internalError(ctx, "Get live network failed", err)
	}
	result := &interfaces.LogicNeedCandidates{NetworkID: network, Candidates: []*interfaces.LogicNeedCandidate{}}
	add := func(c *interfaces.LogicNeedCandidate) {
		if c.Reasons = candidateReasons(need, c.Name, c.OwnerObjectTypeID); len(c.Reasons) > 0 {
			result.Candidates = append(result.Candidates, c)
		}
	}
	reader := s.liveReader(ctx, token, network)
	linkType := interfaces.LogicLinkTypeOf(need.Kind)
	switch linkType {
	case interfaces.LOGIC_LINK_OBJECT_METRIC:
		metrics, lerr := s.bba.ListObjectMetrics(ctx, token, network, "")
		err = lerr
		for _, m := range metrics {
			add(&interfaces.LogicNeedCandidate{Type: linkType, ID: m.ID, Name: m.Name, Description: m.Description,
				OwnerObjectTypeID: m.OwnerObjectTypeID})
		}
	case interfaces.LOGIC_LINK_FUNCTION, interfaces.LOGIC_LINK_SKILL, interfaces.LOGIC_LINK_MCP_TOOL:
		capabilities, lerr := reader.listCapabilities(linkType, capabilityMetadataType(need.Kind))
		err = lerr
		for _, c := range capabilities {
			add(&interfaces.LogicNeedCandidate{Type: linkType, ID: c.CapabilityID, BoxID: c.BoxID, Name: c.Name,
				Description: c.Description})
		}
	case interfaces.LOGIC_LINK_ACTION_TYPE:
		actionTypes, lerr := reader.listActionTypes()
		err = lerr
		for _, a := range actionTypes {
			add(&interfaces.LogicNeedCandidate{Type: linkType, ID: a.ID, Name: a.Name, OwnerObjectTypeID: a.ObjectTypeID})
		}
	}
	if err != nil {
		result.Reason = unreadable(err).Reason
	}
	slices.SortStableFunc(result.Candidates, func(a, b *interfaces.LogicNeedCandidate) int { return candidateRank(b) - candidateRank(a) })
	result.Candidates = result.Candidates[:min(len(result.Candidates), maxCandidates)]
	span.SetStatus(codes.Ok, "")
	return result, nil
}

func (s *requirementService) LinkLogicNeed(ctx context.Context, token string, projectID string, needID string,
	link interfaces.RequirementLogicLinkEdit, caller interfaces.AccountInfo) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "LinkLogicNeed")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return nil, err
	}
	link.ID, link.BoxID = strings.TrimSpace(link.ID), strings.TrimSpace(link.BoxID)
	if link.ID == "" {
		span.SetStatus(codes.Error, "No id")
		return nil, invalidItem(ctx, "id is empty")
	}
	if (link.Type == interfaces.LOGIC_LINK_FUNCTION || link.Type == interfaces.LOGIC_LINK_MCP_TOOL) && link.BoxID == "" {
		span.SetStatus(codes.Error, "No box id")
		return nil, invalidItem(ctx, "box_id is empty")
	}
	// The need and the kind it links to are checked before the network is read.
	read, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	if !ok {
		span.SetStatus(codes.Error, "Requirement not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)
	}
	i, found := find(read.Spec.LogicNeeds, needID, logicNeedID)
	if !found {
		span.SetStatus(codes.Error, "Need not found")
		return nil, notFound(ctx)
	}
	kind := read.Spec.LogicNeeds[i].Kind
	if want := interfaces.LogicLinkTypeOf(kind); want == "" || want != link.Type {
		span.SetStatus(codes.Error, "Wrong link type")
		return nil, invalidItem(ctx, "a "+kind+" need does not link to a "+link.Type)
	}
	network, err := logics.LiveNetwork(ctx, s.rla, entry.KNID)
	if err != nil {
		span.SetStatus(codes.Error, "Get live network failed")
		return nil, internalError(ctx, "Get live network failed", err)
	}
	// Only an implementation the live network has, and the caller can read, is linked.
	target := &interfaces.RequirementLogicLink{Type: link.Type, ID: link.ID, BoxID: link.BoxID, By: caller, At: s.now()}
	if impl := s.liveReader(ctx, token, network).read(target, kind); !impl.Available {
		span.SetStatus(codes.Error, "Implementation not readable")
		if impl.Reason == interfaces.LOGIC_IMPLEMENTATION_UNAVAILABLE {
			return nil, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, perrors.BknProject_Requirement_ImplementationUnavailable)
		}
		return nil, invalidItem(ctx, link.Type+" "+link.ID+" is "+impl.Reason+" in the live network")
	}
	req, err := s.update(ctx, token, projectID, link.UpdateTime, caller,
		func(spec *interfaces.RequirementSpec, _ *draftedItems) error {
			i, ok := find(spec.LogicNeeds, needID, logicNeedID)
			if !ok {
				return notFound(ctx)
			}
			n := spec.LogicNeeds[i]
			// Its kind may have been changed since it was read.
			if n.Kind != kind {
				return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Stale)
			}
			n.Link, n.Accepted = target, true
			return nil
		})
	if err != nil {
		span.SetStatus(codes.Error, "Link need failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return req, nil
}

func (s *requirementService) UnlinkLogicNeed(ctx context.Context, token string, projectID string, needID string,
	updateTime int64, caller interfaces.AccountInfo) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UnlinkLogicNeed")
	defer span.End()

	req, err := s.update(ctx, token, projectID, updateTime, caller,
		func(spec *interfaces.RequirementSpec, _ *draftedItems) error {
			i, ok := find(spec.LogicNeeds, needID, logicNeedID)
			if !ok {
				return notFound(ctx)
			}
			spec.LogicNeeds[i].Link = nil
			return nil
		})
	if err != nil {
		span.SetStatus(codes.Error, "Unlink need failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return req, nil
}
