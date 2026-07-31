package assemblysvc

import (
	"context"
	"errors"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ibusinessresolver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

var ErrNotFound = errors.New("interaction was not found in the authorized scope")

type QueryService struct {
	sessions         isessionstore.Store
	ledger           ievidenceledger.Store
	businessResolver ibusinessresolver.BusinessResolverPort
}

type InteractionView struct {
	Interaction sessionvo.Interaction       `json:"interaction"`
	Revision    *sessionvo.AssemblyRevision `json:"assembly_revision,omitempty"`
	Assembly    ProjectedResult             `json:"assembly"`
}

type ProjectedResult struct {
	Completeness           sessionvo.EvidenceStatus    `json:"completeness"`
	Claims                 []ClaimAssembly             `json:"claims"`
	Events                 []EventNode                 `json:"events"`
	BusinessRefs           []BusinessRefView           `json:"business_refs"`
	ArtifactRefs           []string                    `json:"artifact_refs"`
	EvidenceRefs           []sessionvo.EvidenceRef     `json:"evidence_refs"`
	OperationBusinessEdges []OperationBusinessEdgeView `json:"operation_business_edges"`
	UnusedEvidenceRefs     []sessionvo.EvidenceRef     `json:"unused_evidence_refs"`
	IncludedEventIDs       []string                    `json:"included_event_ids"`
	EventLayers            map[string]int              `json:"event_layers"`
	PartialReasons         []string                    `json:"partial_reasons"`
}

type BusinessRefView struct {
	TechnicalRef     sessionvo.BusinessRef       `json:"technical_ref"`
	DisclosureStatus string                      `json:"disclosure_status"`
	Display          *evidencevo.BusinessDisplay `json:"display,omitempty"`
}

type OperationBusinessEdgeView struct {
	OperationID string                          `json:"operation_id"`
	BusinessRef BusinessRefView                 `json:"business_ref"`
	Role        sessionvo.OperationBusinessRole `json:"role"`
	ObservedAt  string                          `json:"observed_at"`
}

func NewQueryService(sessions isessionstore.Store, ledger ievidenceledger.Store) *QueryService {
	return &QueryService{sessions: sessions, ledger: ledger}
}

func NewQueryServiceWithBusinessResolver(
	sessions isessionstore.Store,
	ledger ievidenceledger.Store,
	resolver ibusinessresolver.BusinessResolverPort,
) *QueryService {
	return &QueryService{sessions: sessions, ledger: ledger, businessResolver: resolver}
}

func (s *QueryService) GetInteraction(ctx context.Context, owner sessionvo.Owner, interactionID string) (InteractionView, error) {
	return s.GetInteractionAuthorized(ctx, owner, interactionID, evidencevo.QueryScope{})
}

func (s *QueryService) GetInteractionAuthorized(
	ctx context.Context,
	owner sessionvo.Owner,
	interactionID string,
	scope evidencevo.QueryScope,
) (InteractionView, error) {
	var interaction sessionvo.Interaction
	var revision *sessionvo.AssemblyRevision
	var claimIDs []string
	err := s.sessions.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		current, found := tx.FindInteraction(interactionID)
		if !found {
			return ErrNotFound
		}
		conversation, found := tx.FindConversation(current.ConversationID)
		if !found || !conversation.Owner.Equal(owner) {
			return ErrNotFound
		}
		interaction = current
		if current.ClosureManifest != nil {
			claimIDs = append([]string(nil), current.ClosureManifest.Claims...)
		}
		revisions := tx.ListAssemblyRevisions(interactionID)
		if len(revisions) > 0 {
			latest := revisions[len(revisions)-1]
			revision = &latest
		}
		return nil
	})
	if err != nil {
		return InteractionView{}, err
	}
	events, err := s.ledger.ListInteractionEvents(ctx, owner, interactionID)
	if err != nil {
		return InteractionView{}, err
	}
	if revision != nil {
		events = eventsInRevision(events, revision.IncludedEventIDs)
	}
	externalEvidence, err := s.loadPriorEvidence(ctx, owner, interaction, events)
	if err != nil {
		return InteractionView{}, err
	}
	assembled := AssembleWithExternalEvidence(interactionID, events, claimIDs, externalEvidence)
	return InteractionView{
		Interaction: interaction, Revision: revision,
		Assembly: s.projectBusinessRefs(ctx, scope, assembled),
	}, nil
}

type priorSupportSource struct {
	support  sessionvo.ClaimSupport
	eventIDs []string
}

func (s *QueryService) loadPriorEvidence(
	ctx context.Context,
	owner sessionvo.Owner,
	current sessionvo.Interaction,
	events []ledgervo.Event,
) ([]sessionvo.EvidenceRef, error) {
	supports := priorSupports(current.ID, events)
	if len(supports) == 0 {
		return nil, nil
	}
	sources := make([]priorSupportSource, 0, len(supports))
	err := s.sessions.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		for _, support := range supports {
			source, found := tx.FindInteraction(support.SourceInteractionID)
			if !found || source.ConversationID != current.ConversationID || source.Ordinal >= current.Ordinal {
				continue
			}
			conversation, found := tx.FindConversation(source.ConversationID)
			if !found || !conversation.Owner.Equal(owner) {
				continue
			}
			for _, revision := range tx.ListAssemblyRevisions(source.ID) {
				if revision.ID == support.SourceRevisionID {
					sources = append(sources, priorSupportSource{
						support: support, eventIDs: append([]string(nil), revision.IncludedEventIDs...),
					})
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	resolved := make(map[string]sessionvo.EvidenceRef)
	for _, source := range sources {
		sourceEvents, err := s.ledger.ListInteractionEvents(ctx, owner, source.support.SourceInteractionID)
		if err != nil {
			return nil, err
		}
		for _, event := range eventsInRevision(sourceEvents, source.eventIDs) {
			for _, ref := range event.EvidenceRefs {
				candidate := map[string]sessionvo.EvidenceRef{evidenceRefKey(ref): ref}
				if ref.Ref == source.support.TargetRef {
					if matched, found := supportMatchesEvidence(source.support, candidate); found {
						resolved[evidenceRefKey(matched)] = matched
					}
				}
			}
		}
	}
	return sortedTypedEvidenceRefs(resolved), nil
}

func priorSupports(currentInteractionID string, events []ledgervo.Event) []sessionvo.ClaimSupport {
	result := make([]sessionvo.ClaimSupport, 0)
	for _, event := range events {
		for _, claim := range event.Claims {
			for _, support := range claim.Supports {
				if support.SourceInteractionID != currentInteractionID {
					result = append(result, support)
				}
			}
		}
	}
	return result
}

func (s *QueryService) projectBusinessRefs(ctx context.Context, scope evidencevo.QueryScope, assembled Result) ProjectedResult {
	projected := ProjectedResult{
		Completeness: assembled.Completeness, Claims: assembled.Claims, Events: assembled.Events,
		ArtifactRefs: assembled.ArtifactRefs, EvidenceRefs: assembled.EvidenceRefs,
		UnusedEvidenceRefs: assembled.UnusedEvidenceRefs,
		IncludedEventIDs:   assembled.IncludedEventIDs, EventLayers: assembled.EventLayers,
		PartialReasons: assembled.PartialReasons,
	}
	resolutions := s.resolveBusinessRefs(ctx, scope, assembled.BusinessRefs)
	viewsByKey := make(map[string]BusinessRefView, len(assembled.BusinessRefs))
	for _, ref := range assembled.BusinessRefs {
		resolution, found := resolutions[ref.RefID]
		if found && resolution.Visibility == "unauthorized" {
			continue
		}
		view := BusinessRefView{TechnicalRef: ref, DisclosureStatus: "unresolved"}
		if found && resolution.Visibility == "visible" && resolution.Display != nil {
			view.DisclosureStatus = "visible"
			view.Display = resolution.Display
		} else {
			view.Display = &evidencevo.BusinessDisplay{Name: ref.RefID, ResolutionStatus: "unresolved"}
		}
		viewsByKey[businessRefKey(ref)] = view
		projected.BusinessRefs = append(projected.BusinessRefs, view)
	}
	for _, edge := range assembled.OperationBusinessEdges {
		view, visible := viewsByKey[businessRefKey(edge.BusinessRef)]
		if !visible {
			continue
		}
		projected.OperationBusinessEdges = append(projected.OperationBusinessEdges, OperationBusinessEdgeView{
			OperationID: edge.OperationID, BusinessRef: view, Role: edge.Role,
			ObservedAt: edge.ObservedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		})
	}
	return projected
}

func (s *QueryService) resolveBusinessRefs(
	ctx context.Context,
	scope evidencevo.QueryScope,
	refs []sessionvo.BusinessRef,
) map[string]ibusinessresolver.Resolution {
	result := make(map[string]ibusinessresolver.Resolution, len(refs))
	if s.businessResolver == nil || len(refs) == 0 {
		return result
	}
	requestRefs := make([]ibusinessresolver.BusinessRef, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if _, found := seen[ref.RefID]; found {
			continue
		}
		seen[ref.RefID] = struct{}{}
		sourceSystem := "bkn"
		if ref.RefType == sessionvo.BusinessRefDataResource {
			sourceSystem = "vega"
		}
		requestRefs = append(requestRefs, ibusinessresolver.BusinessRef{
			RefID: ref.RefID, RefType: string(ref.RefType), SourceSystem: sourceSystem,
			VersionStatus: ref.Version,
		})
	}
	sort.Slice(requestRefs, func(i, j int) bool { return requestRefs[i].RefID < requestRefs[j].RefID })
	resolved, err := s.businessResolver.ResolveBusinessRefs(ctx, ibusinessresolver.ResolveRequest{Scope: scope, Refs: requestRefs})
	if err != nil {
		return result
	}
	for _, resolution := range resolved {
		result[resolution.RefID] = resolution
	}
	return result
}

func eventsInRevision(events []ledgervo.Event, includedIDs []string) []ledgervo.Event {
	included := make(map[string]struct{}, len(includedIDs))
	for _, eventID := range includedIDs {
		included[eventID] = struct{}{}
	}
	result := make([]ledgervo.Event, 0, len(included))
	for _, event := range events {
		if _, found := included[event.EventID]; found {
			result = append(result, event)
		}
	}
	return result
}
