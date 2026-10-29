// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// GetPending lists what waits for a person before the draft can be confirmed, to whoever sees the
// project. A draft not ready yet has nothing to decide.
func (s *draftService) GetPending(ctx context.Context, token string, projectID string) (*interfaces.ProjectPending, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetPending")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist {
		span.SetStatus(codes.Error, "Draft not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
	}
	if draft.Status != interfaces.DRAFT_STATUS_READY {
		span.SetStatus(codes.Ok, "")
		return &interfaces.ProjectPending{
			ProjectID: projectID, DraftUpdateTime: draft.UpdateTime, Items: []interfaces.PendingItem{},
		}, nil
	}
	needed, err := s.neededBy(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return pendingOf(entry.Project, draft, needed), nil
}

// neededBy maps each drafted item a knowledge question of the project's ready requirement
// specification needs, by kind and item ID, to those questions; with no ready specification, none.
func (s *draftService) neededBy(ctx context.Context, projectID string) (map[string][]interfaces.PendingQuestion, error) {
	req, ok, err := s.rqa.GetRequirement(ctx, projectID)
	if err != nil {
		return nil, err
	}
	needed := map[string][]interfaces.PendingQuestion{}
	if !ok || req.Status != interfaces.REQUIREMENT_STATUS_READY {
		return needed, nil
	}
	kinds := map[string]string{
		interfaces.REQUIREMENT_ITEM_OBJECT_TYPE:   interfaces.DRAFT_KIND_OBJECT_TYPES,
		interfaces.REQUIREMENT_ITEM_RELATION_TYPE: interfaces.DRAFT_KIND_RELATION_TYPES,
	}
	for _, q := range req.Spec.Questions {
		if q.Layer != interfaces.REQUIREMENT_LAYER_KNOWLEDGE {
			continue
		}
		for _, need := range q.Needs {
			ref := kinds[need.Kind] + "/" + need.ID
			if !slices.ContainsFunc(needed[ref], func(p interfaces.PendingQuestion) bool { return p.ID == q.ID }) {
				needed[ref] = append(needed[ref], interfaces.PendingQuestion{ID: q.ID, Text: q.Text})
			}
		}
	}
	return needed, nil
}

// pendingOf lists what waits for a person before the draft can be confirmed — the items
// pendingItems finds, the check confirming makes — each with the requests that settle it and the
// questions that need it. Items come by kind, then item ID. No written rule recommends an option
// yet, so none is recommended.
func pendingOf(project *interfaces.Project, draft *interfaces.Draft,
	needed map[string][]interfaces.PendingQuestion) *interfaces.ProjectPending {
	p := &interfaces.ProjectPending{
		ProjectID: project.ID, DraftUpdateTime: draft.UpdateTime, Items: []interfaces.PendingItem{},
	}
	base := "/projects/" + project.ID + "/ontology/draft"
	option := func(action string, method string, path string, body map[string]any) interfaces.PendingOption {
		body["update_time"] = interfaces.PENDING_PLACEHOLDER_UPDATE_TIME
		o := interfaces.PendingOption{Action: action, Method: method, Path: path, Body: body, Allowed: true}
		if project.Archived {
			o.Allowed, o.Reason = false, interfaces.ACTION_REASON_ARCHIVED
		}
		return o
	}
	setAdopted := func(kind string, id string, adopted bool, action string) interfaces.PendingOption {
		return option(action, http.MethodPatch, base+"/items/"+kind+"/"+id, map[string]any{"adopted": adopted})
	}

	// Choosing an identity among the properties that can be one; with none, the object type can only
	// be left out.
	chooseIdentity := func(ot *interfaces.DraftObjectType) interfaces.PendingOption {
		choose := option(interfaces.ACTION_CHOOSE_IDENTITY, http.MethodPut, base+"/object_types/"+ot.ID+"/identity",
			map[string]any{"properties": interfaces.PENDING_PLACEHOLDER_CANDIDATE})
		choose.Candidates = identityChoices(ot)
		if choose.Allowed && len(choose.Candidates) == 0 {
			choose.Allowed, choose.Reason = false, interfaces.ACTION_REASON_NO_KEY_PROPERTY
		}
		return choose
	}

	pending := pendingItems(&draft.Items)
	needsIdentity := pending[interfaces.BLOCKER_NEEDS_IDENTITY]
	unadoptedEnds := pending[interfaces.BLOCKER_UNADOPTED_ENDS]

	objectTypes := slices.Clone(draft.Items.ObjectTypes)
	slices.SortFunc(objectTypes, func(a, b *interfaces.DraftObjectType) int { return strings.Compare(a.ID, b.ID) })
	for _, ot := range objectTypes {
		if !slices.Contains(needsIdentity, ot.ID) {
			continue
		}
		choose := chooseIdentity(ot)
		p.Items = append(p.Items, interfaces.PendingItem{
			Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: ot.ID, Name: ot.Name, Blocking: true,
			Reason: interfaces.BLOCKER_NEEDS_IDENTITY, Evidence: ot.Key,
			Options: []interfaces.PendingOption{
				choose,
				setAdopted(interfaces.DRAFT_KIND_OBJECT_TYPES, ot.ID, false, interfaces.ACTION_UNADOPT),
			},
		})
	}

	relationTypes := slices.Clone(draft.Items.RelationTypes)
	slices.SortFunc(relationTypes, func(a, b *interfaces.DraftRelationType) int { return strings.Compare(a.ID, b.ID) })
	for _, rt := range relationTypes {
		if !slices.Contains(unadoptedEnds, rt.ID) {
			continue
		}
		options := []interfaces.PendingOption{}
		seen := map[string]bool{}
		for _, end := range []string{rt.Source, rt.Target} {
			ot := findObjectType(&draft.Items, end)
			// A relation of an object type to itself has one end to adopt.
			if ot == nil || ot.Adopted || seen[end] {
				continue
			}
			seen[end] = true
			adopt := setAdopted(interfaces.DRAFT_KIND_OBJECT_TYPES, end, true, interfaces.ACTION_ADOPT_END)
			// Adopting an object type that waits for its identity is refused; that comes first.
			if adopt.Allowed && ot.NeedsIdentity {
				adopt.Allowed, adopt.Reason = false, interfaces.ACTION_REASON_END_NEEDS_IDENTITY
			}
			options = append(options, adopt)
		}
		options = append(options, setAdopted(interfaces.DRAFT_KIND_RELATION_TYPES, rt.ID, false, interfaces.ACTION_UNADOPT))
		p.Items = append(p.Items, interfaces.PendingItem{
			Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: rt.ID, Name: rt.Name, Blocking: true,
			Reason: interfaces.BLOCKER_UNADOPTED_ENDS, Evidence: rt.Evidence, Options: options,
		})
	}

	// Then what drafting left out and no one decided on: taking it in is the person's choice, and
	// confirming without leaves it out only when accepted.
	for _, u := range undecidedItems(&draft.Items) {
		item := interfaces.PendingItem{
			Kind: u.Kind, ItemID: u.ItemID, Reason: interfaces.PENDING_REASON_UNDECIDED, Because: u.Because,
		}
		adopt := setAdopted(u.Kind, u.ItemID, true, interfaces.ACTION_ADOPT)
		if u.Kind == interfaces.DRAFT_KIND_OBJECT_TYPES {
			ot := findObjectType(&draft.Items, u.ItemID)
			item.Name, item.Evidence = ot.Name, ot.Key
			if ot.NeedsIdentity {
				item.Options = append(item.Options, chooseIdentity(ot))
				if adopt.Allowed {
					adopt.Allowed, adopt.Reason = false, interfaces.BLOCKER_NEEDS_IDENTITY
				}
			}
		} else {
			rt := findRelationType(&draft.Items, u.ItemID)
			item.Name, item.Evidence = rt.Name, rt.Evidence
			if adopt.Allowed && !(objectTypeAdopted(&draft.Items, rt.Source) && objectTypeAdopted(&draft.Items, rt.Target)) {
				adopt.Allowed, adopt.Reason = false, interfaces.BLOCKER_UNADOPTED_ENDS
			}
		}
		item.Options = append(item.Options, adopt)
		p.Items = append(p.Items, item)
	}
	for i := range p.Items {
		p.Items[i].NeededBy = needed[p.Items[i].Kind+"/"+p.Items[i].ItemID]
	}
	return p
}

// identityChoices lists the properties that may identify an object type's records, one each:
// the field drafting counted first, then the other key-typed properties by name. Several
// properties together are a person's to choose; drafting counted none of them.
func identityChoices(ot *interfaces.DraftObjectType) [][]string {
	var counted string
	names := []string{}
	for _, p := range ot.Properties {
		if !keyTypes[p.Type] {
			continue
		}
		if ot.Key.Field != "" && p.MappedField == ot.Key.Field {
			counted = p.Name
			continue
		}
		names = append(names, p.Name)
	}
	slices.Sort(names)
	candidates := [][]string{}
	if counted != "" {
		candidates = append(candidates, []string{counted})
	}
	for _, name := range names {
		candidates = append(candidates, []string{name})
	}
	return candidates
}
