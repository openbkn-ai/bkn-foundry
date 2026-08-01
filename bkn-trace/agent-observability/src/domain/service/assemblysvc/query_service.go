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

var errBusinessResolverUnavailable = errors.New("business resolver is unavailable")

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
	// UnusedEvidenceRefs are refs neither adopted nor rejected by any Claim in this revision.
	UnusedEvidenceRefs []sessionvo.EvidenceRef `json:"unused_evidence_refs"`
	IncludedEventIDs   []string                `json:"included_event_ids"`
	EventLayers        map[string]int          `json:"event_layers"`
	// PartialReasons describe objective evidence-assembly gaps and never authorization outcomes.
	PartialReasons []string `json:"partial_reasons"`
	// DisclosurePartial is true when the current authorized projection could not classify every business ref.
	DisclosurePartial bool `json:"disclosure_partial"`
	// DisclosureReasons describe resolver/projection degradation without changing objective completeness.
	DisclosureReasons []string `json:"disclosure_reasons"`
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
	var recordOwner sessionvo.Owner
	var revision *sessionvo.AssemblyRevision
	var claimIDs []string
	err := s.sessions.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		current, found := tx.FindInteraction(interactionID)
		if !found {
			return ErrNotFound
		}
		conversation, found := tx.FindConversation(current.ConversationID)
		if !found {
			return ErrNotFound
		}
		if scope.AccessProfile == nil && !conversation.Owner.Equal(owner) {
			return ErrNotFound
		}
		recordOwner = conversation.Owner
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
	events, err := s.ledger.ListInteractionEvents(ctx, recordOwner, interactionID)
	if err != nil {
		return InteractionView{}, err
	}
	if revision != nil {
		events = eventsInRevision(events, revision.IncludedEventIDs)
	}
	if scope.AccessProfile != nil && !evidencevo.CanReadRecord(
		*scope.AccessProfile,
		interactionRecordScope(recordOwner, events),
		evidencevo.AccessViewBusiness,
	) {
		return InteractionView{}, ErrNotFound
	}
	externalEvidence, err := s.loadPriorEvidence(ctx, recordOwner, interaction, events)
	if err != nil {
		return InteractionView{}, err
	}
	assembled := AssembleWithExternalEvidence(interactionID, events, claimIDs, externalEvidence)
	return InteractionView{
		Interaction: interaction, Revision: revision,
		Assembly: s.projectBusinessRefs(ctx, scope, assembled),
	}, nil
}

func interactionRecordScope(owner sessionvo.Owner, events []ledgervo.Event) evidencevo.RecordScope {
	// Ledger BusinessRefs are the validated authorization boundary; the opaque Envelope is not scanned.
	refs := make([]string, 0)
	for _, event := range events {
		for _, ref := range event.BusinessRefs {
			refs = append(refs, ref.RefID)
		}
	}
	return evidencevo.RecordScope{
		TenantID: owner.TenantID, BusinessDomain: owner.BusinessDomainID,
		EffectiveSubjectID:     owner.EffectiveSubjectID,
		ApplicationPrincipalID: owner.ApplicationPrincipalID,
		KnowledgeNetworkIDs:    evidencevo.KnowledgeNetworkIDsFromRefs(refs),
	}
}

type priorSupportSource struct {
	interactionID string
	eventIDs      []string
	supports      []sessionvo.ClaimSupport
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
	sourcesByRevision := make(map[string]*priorSupportSource)
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
					key := source.ID + "\x00" + revision.ID
					entry := sourcesByRevision[key]
					if entry == nil {
						entry = &priorSupportSource{
							interactionID: source.ID,
							eventIDs:      append([]string(nil), revision.IncludedEventIDs...),
						}
						sourcesByRevision[key] = entry
					}
					entry.supports = append(entry.supports, support)
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
	sourceKeys := make([]string, 0, len(sourcesByRevision))
	for key := range sourcesByRevision {
		sourceKeys = append(sourceKeys, key)
	}
	sort.Strings(sourceKeys)
	eventsByInteraction := make(map[string][]ledgervo.Event)
	for _, key := range sourceKeys {
		source := sourcesByRevision[key]
		sourceEvents, found := eventsByInteraction[source.interactionID]
		if !found {
			var err error
			sourceEvents, err = s.ledger.ListInteractionEvents(ctx, owner, source.interactionID)
			if err != nil {
				return nil, err
			}
			eventsByInteraction[source.interactionID] = sourceEvents
		}
		candidates := make(map[string]sessionvo.EvidenceRef)
		for _, event := range eventsInRevision(sourceEvents, source.eventIDs) {
			for _, ref := range event.EvidenceRefs {
				candidates[evidenceRefKey(ref)] = ref
			}
		}
		for _, support := range source.supports {
			for _, matched := range matchingEvidence(support, candidates) {
				resolved[evidenceRefKey(matched)] = matched
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
		BusinessRefs: []BusinessRefView{}, OperationBusinessEdges: []OperationBusinessEdgeView{},
		ArtifactRefs: assembled.ArtifactRefs, EvidenceRefs: assembled.EvidenceRefs,
		UnusedEvidenceRefs: assembled.UnusedEvidenceRefs,
		IncludedEventIDs:   assembled.IncludedEventIDs, EventLayers: assembled.EventLayers,
		PartialReasons: assembled.PartialReasons,
	}
	resolutions, resolveErr := s.resolveBusinessRefs(ctx, scope, assembled.BusinessRefs)
	disclosureReasons := make(map[string]struct{})
	if resolveErr != nil {
		disclosureReasons["business_resolver_unavailable"] = struct{}{}
	}
	viewsByKey := make(map[string]BusinessRefView, len(assembled.BusinessRefs))
	for _, ref := range assembled.BusinessRefs {
		resolution, found := resolutions[resolverRefKey(string(ref.RefType), ref.RefID, sourceSystemForBusinessRef(ref))]
		status := "unresolved"
		var display *evidencevo.BusinessDisplay
		if found && resolution.Display != nil {
			display = resolution.Display
			status = resolution.Display.ResolutionStatus
			if status == "" {
				status = "resolved"
			}
		} else {
			disclosureReasons["business_ref_unresolved"] = struct{}{}
		}
		view := BusinessRefView{TechnicalRef: ref, DisclosureStatus: status, Display: display}
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
	projected.DisclosureReasons = sortedStringSet(disclosureReasons)
	projected.DisclosurePartial = len(projected.DisclosureReasons) > 0
	return projected
}

func (s *QueryService) resolveBusinessRefs(
	ctx context.Context,
	scope evidencevo.QueryScope,
	refs []sessionvo.BusinessRef,
) (map[string]ibusinessresolver.Resolution, error) {
	result := make(map[string]ibusinessresolver.Resolution, len(refs))
	if len(refs) == 0 {
		return result, nil
	}
	if s.businessResolver == nil {
		return result, errBusinessResolverUnavailable
	}
	requestRefs := make([]ibusinessresolver.BusinessRef, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		sourceSystem := sourceSystemForBusinessRef(ref)
		key := resolverRefKey(string(ref.RefType), ref.RefID, sourceSystem)
		if _, found := seen[key]; found {
			continue
		}
		seen[key] = struct{}{}
		requestRefs = append(requestRefs, ibusinessresolver.BusinessRef{
			RefID: ref.RefID, RefType: string(ref.RefType), SourceSystem: sourceSystem,
			VersionStatus: ref.Version,
		})
	}
	sort.Slice(requestRefs, func(i, j int) bool {
		return resolverRefKey(requestRefs[i].RefType, requestRefs[i].RefID, requestRefs[i].SourceSystem) <
			resolverRefKey(requestRefs[j].RefType, requestRefs[j].RefID, requestRefs[j].SourceSystem)
	})
	resolved, err := s.businessResolver.ResolveBusinessRefs(ctx, ibusinessresolver.ResolveRequest{Scope: scope, Refs: requestRefs})
	if err != nil {
		return result, err
	}
	for _, resolution := range resolved {
		result[resolverRefKey(resolution.RefType, resolution.RefID, resolution.SourceSystem)] = resolution
	}
	return result, nil
}

func sourceSystemForBusinessRef(ref sessionvo.BusinessRef) string {
	if ref.RefType == sessionvo.BusinessRefDataResource {
		return "vega"
	}
	return "bkn"
}

func resolverRefKey(refType, refID, sourceSystem string) string {
	return refType + "\x00" + refID + "\x00" + sourceSystem
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
