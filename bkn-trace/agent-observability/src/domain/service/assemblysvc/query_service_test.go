package assemblysvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/assemblysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ibusinessresolver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type queryResolver struct {
	resolutions []ibusinessresolver.Resolution
	requests    []ibusinessresolver.ResolveRequest
	err         error
}

type countingLedger struct {
	*ledgerstore.Store
	reads map[string]int
}

func (s *countingLedger) ListInteractionEvents(
	ctx context.Context,
	owner sessionvo.Owner,
	interactionID string,
) ([]ledgervo.Event, error) {
	s.reads[interactionID]++
	return s.Store.ListInteractionEvents(ctx, owner, interactionID)
}

func (r *queryResolver) ResolveBusinessRefs(_ context.Context, request ibusinessresolver.ResolveRequest) ([]ibusinessresolver.Resolution, error) {
	r.requests = append(r.requests, request)
	return r.resolutions, r.err
}

func TestQueryUsesLatestImmutableRevisionEventSetAndEnforcesOwner(t *testing.T) {
	t.Parallel()

	sessions := sessionstore.New()
	lifecycle := sessionsvc.New(sessions, sessionsvc.Options{})
	ledger := ledgerstore.New()
	owner := sessionvo.Owner{
		TenantID: "tenant-1", BusinessDomainID: "domain-1",
		ApplicationPrincipalID: "app-1", EffectiveSubjectType: sessionvo.SubjectService,
		EffectiveSubjectID: "agent-1",
	}
	conversation, err := lifecycle.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "thread-1", IdempotencyKey: "conv-1",
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	interaction, err := lifecycle.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "int-1",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}

	first := semanticEvent("evt-first", "op-query", 1)
	first.Owner = owner
	first.ConversationID = conversation.ID
	first.InteractionID = interaction.ID
	first.EvidenceRefs = []sessionvo.EvidenceRef{evidenceRef("evidence:june")}
	first.Claims = []sessionvo.Claim{claim("claim-a", sessionvo.SupportAdopted, "")}
	first.Claims[0].Supports[0].SourceInteractionID = interaction.ID
	first.EvidenceRefs[0].SourceInteractionID = interaction.ID
	late := semanticEvent("evt-late", "op-late", 1)
	late.Owner = owner
	late.ConversationID = conversation.ID
	late.InteractionID = interaction.ID
	if _, err := ledger.Commit(context.Background(), first); err != nil {
		t.Fatalf("commit first event: %v", err)
	}
	if _, err := ledger.Commit(context.Background(), late); err != nil {
		t.Fatalf("commit late event: %v", err)
	}

	seedRevision(t, sessions, interaction, []string{"claim-a"}, []string{"evt-first"})

	view, err := assemblysvc.NewQueryService(sessions, ledger).GetInteraction(context.Background(), owner, interaction.ID)
	if err != nil {
		t.Fatalf("query semantic graph: %v", err)
	}
	if len(view.Assembly.IncludedEventIDs) != 1 || view.Assembly.IncludedEventIDs[0] != "evt-first" {
		t.Fatalf("late event changed an immutable revision: %#v", view.Assembly.IncludedEventIDs)
	}

	otherOwner := owner
	otherOwner.EffectiveSubjectID = "agent-2"
	if _, err := assemblysvc.NewQueryService(sessions, ledger).GetInteraction(context.Background(), otherOwner, interaction.ID); err != assemblysvc.ErrNotFound {
		t.Fatalf("foreign owner must receive non-disclosure, got %v", err)
	}
}

func TestQueryAuthorizesManagedKnowledgeNetworkInteraction(t *testing.T) {
	t.Parallel()

	sessions, ledger, owner, interaction := queryFixture(t)
	profile := evidencevo.AccessProfile{
		TenantID: "tenant-1", BusinessDomain: "domain-1", EffectiveSubjectID: "builder-1",
		Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"supplychain"},
		AccountActive: true, TenantActive: true,
	}
	requester := owner
	requester.EffectiveSubjectID = profile.EffectiveSubjectID
	view, err := assemblysvc.NewQueryService(sessions, ledger).GetInteractionAuthorized(
		context.Background(), requester, interaction.ID,
		evidencevo.QueryScope{AccessProfile: &profile, View: evidencevo.AccessViewBusiness},
	)
	if err != nil {
		t.Fatalf("network builder query managed interaction: %v", err)
	}
	if view.Interaction.ID != interaction.ID {
		t.Fatalf("unexpected interaction: %#v", view.Interaction)
	}
}

func TestQueryReturnsCompleteManagedInteractionWithoutPerEdgeAuthorization(t *testing.T) {
	t.Parallel()

	sessions, ledger, owner, interaction := queryFixture(t)
	edgeOnly := semanticEvent("evt-edge-only", "op-edge-only", 2)
	edgeOnly.Owner = owner
	edgeOnly.ConversationID = interaction.ConversationID
	edgeOnly.InteractionID = interaction.ID
	edgeOnly.BusinessRefs = nil
	edgeOnly.OperationBusinessEdges = []sessionvo.OperationBusinessEdge{{
		OperationID: edgeOnly.OperationID,
		BusinessRef: sessionvo.BusinessRef{
			RefType: sessionvo.BusinessRefObjectType, RefID: "object:restricted:salary",
			BusinessDomainID: owner.BusinessDomainID, Version: "2026.07",
		},
		Role: sessionvo.OperationRoleRead, ObservedAt: edgeOnly.ObservedAt,
	}}
	if _, err := ledger.Commit(context.Background(), edgeOnly); err != nil {
		t.Fatalf("commit edge-only business ref: %v", err)
	}

	profile := evidencevo.AccessProfile{
		TenantID: "tenant-1", BusinessDomain: "domain-1", EffectiveSubjectID: "builder-1",
		Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"supplychain"},
		AccountActive: true, TenantActive: true,
	}
	requester := owner
	requester.EffectiveSubjectID = profile.EffectiveSubjectID
	view, err := assemblysvc.NewQueryService(sessions, ledger).GetInteractionAuthorized(
		context.Background(), requester, interaction.ID,
		evidencevo.QueryScope{AccessProfile: &profile, View: evidencevo.AccessViewBusiness},
	)
	if err != nil {
		t.Fatalf("managed Trace must be returned without per-edge authorization: %v", err)
	}
	if view.Interaction.ID != interaction.ID {
		t.Fatalf("unexpected interaction: %#v", view.Interaction)
	}
}

func TestQueryRejectsInteractionOutsideManagedKnowledgeNetworkScope(t *testing.T) {
	t.Parallel()

	sessions, ledger, owner, interaction := queryFixture(t)
	tests := map[string]evidencevo.AccessProfile{
		"partial network scope": {
			TenantID: "tenant-1", BusinessDomain: "domain-1", EffectiveSubjectID: "builder-1",
			Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"other-network"},
			AccountActive: true, TenantActive: true,
		},
	}
	for name, profile := range tests {
		name, profile := name, profile
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requester := owner
			requester.EffectiveSubjectID = profile.EffectiveSubjectID
			_, err := assemblysvc.NewQueryService(sessions, ledger).GetInteractionAuthorized(
				context.Background(), requester, interaction.ID,
				evidencevo.QueryScope{AccessProfile: &profile, View: evidencevo.AccessViewBusiness},
			)
			if err != assemblysvc.ErrNotFound {
				t.Fatalf("out-of-scope interaction must use non-disclosure response, got %v", err)
			}
		})
	}

	admin := evidencevo.AccessProfile{
		TenantID: "tenant-1", BusinessDomain: "domain-1", EffectiveSubjectID: "admin-1",
		Roles: []string{"admin"}, AccountActive: true, TenantActive: true,
	}
	requester := owner
	requester.EffectiveSubjectID = admin.EffectiveSubjectID
	if _, err := assemblysvc.NewQueryService(sessions, ledger).GetInteractionAuthorized(
		context.Background(), requester, interaction.ID,
		evidencevo.QueryScope{AccessProfile: &admin, View: evidencevo.AccessViewBusiness},
	); err != nil {
		t.Fatalf("admin must read the complete business Trace, got %v", err)
	}
}

func TestQueryProjectsResolvedBusinessNamesWithoutChangingRecordAuthorization(t *testing.T) {
	t.Parallel()

	sessions, ledger, owner, interaction := queryFixture(t)
	resolver := &queryResolver{resolutions: []ibusinessresolver.Resolution{
		{
			RefID: "object:supplychain:forecast", RefType: "object_type", SourceSystem: "bkn", Visibility: "visible",
			Display: &evidencevo.BusinessDisplay{
				Name: "需求预测单", BusinessPath: []string{"HD供应链业务知识网络_v3", "需求预测单"},
				ResolutionStatus: "resolved", SourceVersion: "main",
			},
		},
		{RefID: "property:supplychain:forecast:qty", RefType: "property", SourceSystem: "bkn", Visibility: "unauthorized"},
	}}
	service := assemblysvc.NewQueryServiceWithBusinessResolver(sessions, ledger, resolver)
	view, err := service.GetInteractionAuthorized(context.Background(), owner, interaction.ID, evidencevo.QueryScope{
		TenantID: "tenant-1", BusinessDomain: "domain-1", AccountID: "agent-1", AccountType: "service",
		Authorization: "Bearer current-user-token",
	})
	if err != nil {
		t.Fatalf("query projected graph: %v", err)
	}
	if view.Assembly.Completeness != sessionvo.EvidenceNotApplicable {
		t.Fatalf("disclosure changed objective assembly completeness: %s", view.Assembly.Completeness)
	}
	if len(view.Assembly.BusinessRefs) != 2 {
		t.Fatalf("resolver visibility must not remove record-authorized technical refs: %#v", view.Assembly.BusinessRefs)
	}
	projected := view.Assembly.BusinessRefs[0]
	if projected.Display == nil || projected.Display.Name != "需求预测单" ||
		projected.TechnicalRef.Version != "2026.07" || projected.DisclosureStatus != "resolved" {
		t.Fatalf("resolved business display or technical ref missing: %#v", projected)
	}
	if view.Assembly.BusinessRefs[1].TechnicalRef.RefID != "property:supplychain:forecast:qty" ||
		view.Assembly.BusinessRefs[1].Display != nil || view.Assembly.BusinessRefs[1].DisclosureStatus != "unresolved" {
		t.Fatalf("unresolved resolver metadata must retain the technical ref without display: %#v", view.Assembly.BusinessRefs[1])
	}
	edgeRefs := map[string]bool{}
	for _, edge := range view.Assembly.OperationBusinessEdges {
		edgeRefs[edge.BusinessRef.TechnicalRef.RefID] = true
	}
	if len(view.Assembly.OperationBusinessEdges) != 2 ||
		!edgeRefs["object:supplychain:forecast"] || !edgeRefs["property:supplychain:forecast:qty"] {
		t.Fatalf("operation edges must retain all record-authorized business refs: %#v", view.Assembly.OperationBusinessEdges)
	}
	if len(resolver.requests) != 1 || resolver.requests[0].Scope.Authorization != "Bearer current-user-token" {
		t.Fatalf("resolver did not receive current authorization scope: %#v", resolver.requests)
	}
}

func TestQueryBusinessProjectionKeepsTechnicalRefsWhenResolverCannotEnrichDisplay(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*sessionstore.Store, *ledgerstore.Store) *assemblysvc.QueryService{
		"resolver error": func(sessions *sessionstore.Store, ledger *ledgerstore.Store) *assemblysvc.QueryService {
			return assemblysvc.NewQueryServiceWithBusinessResolver(sessions, ledger, &queryResolver{err: errors.New("resolver unavailable")})
		},
		"unresolved refs": func(sessions *sessionstore.Store, ledger *ledgerstore.Store) *assemblysvc.QueryService {
			return assemblysvc.NewQueryServiceWithBusinessResolver(sessions, ledger, &queryResolver{resolutions: []ibusinessresolver.Resolution{
				{RefID: "object:supplychain:forecast", RefType: "object_type", SourceSystem: "bkn", Visibility: "unresolved"},
				{RefID: "property:supplychain:forecast:qty", RefType: "property", SourceSystem: "bkn", Visibility: "unresolved"},
			}})
		},
		"resolver disabled": func(sessions *sessionstore.Store, ledger *ledgerstore.Store) *assemblysvc.QueryService {
			return assemblysvc.NewQueryService(sessions, ledger)
		},
	}
	for name, newService := range tests {
		name, newService := name, newService
		t.Run(name, func(t *testing.T) {
			sessions, ledger, owner, interaction := queryFixture(t)
			view, err := newService(sessions, ledger).GetInteractionAuthorized(
				context.Background(), owner, interaction.ID, evidencevo.QueryScope{Authorization: "Bearer token"},
			)
			if err != nil {
				t.Fatalf("query projected graph: %v", err)
			}
			if len(view.Assembly.BusinessRefs) != 2 || len(view.Assembly.OperationBusinessEdges) != 2 {
				t.Fatalf("resolver degradation must not remove record-authorized refs or edges: %#v", view.Assembly)
			}
			if view.Assembly.BusinessRefs == nil || view.Assembly.OperationBusinessEdges == nil {
				t.Fatalf("empty projected collections must serialize as arrays: %#v", view.Assembly)
			}
			if !view.Assembly.DisclosurePartial || len(view.Assembly.DisclosureReasons) == 0 {
				t.Fatalf("resolver degradation was not disclosed separately: %#v", view.Assembly)
			}
			if view.Assembly.Completeness != sessionvo.EvidenceNotApplicable {
				t.Fatalf("authorization degradation changed objective completeness: %s", view.Assembly.Completeness)
			}
		})
	}
}

func TestQueryDoesNotReuseAuthorizationAcrossBusinessRefTypes(t *testing.T) {
	t.Parallel()

	sessions, ledger, owner, interaction := queryFixture(t)
	sharedID := "object:supplychain:forecast"
	second := semanticEvent("evt-type-confusion", "op-query", 2)
	second.Owner = owner
	second.ConversationID = interaction.ConversationID
	second.InteractionID = interaction.ID
	second.BusinessRefs = []sessionvo.BusinessRef{{
		RefType: sessionvo.BusinessRefProperty, RefID: sharedID,
		BusinessDomainID: owner.BusinessDomainID, Version: "2026.07",
	}}
	if _, err := ledger.Commit(context.Background(), second); err != nil {
		t.Fatalf("commit type-confused ref: %v", err)
	}
	resolver := &queryResolver{resolutions: []ibusinessresolver.Resolution{{
		RefID: sharedID, RefType: "object_type", SourceSystem: "bkn", Visibility: "visible",
		Display: &evidencevo.BusinessDisplay{Name: "需求预测单", ResolutionStatus: "resolved"},
	}}}
	view, err := assemblysvc.NewQueryServiceWithBusinessResolver(sessions, ledger, resolver).
		GetInteractionAuthorized(context.Background(), owner, interaction.ID, evidencevo.QueryScope{})
	if err != nil {
		t.Fatalf("query projected graph: %v", err)
	}
	if len(view.Assembly.BusinessRefs) != 3 {
		t.Fatalf("resolver display matching must not remove technical refs of another type: %#v", view.Assembly.BusinessRefs)
	}
}

func TestQueryAllowsOnlyEarlierImmutableClaimSupportFromSameConversation(t *testing.T) {
	t.Parallel()

	sessions := sessionstore.New()
	lifecycle := sessionsvc.New(sessions, sessionsvc.Options{})
	ledger := ledgerstore.New()
	owner := sessionvo.Owner{
		TenantID: "tenant-1", BusinessDomainID: "domain-1", ApplicationPrincipalID: "app-1",
		EffectiveSubjectType: sessionvo.SubjectService, EffectiveSubjectID: "agent-1",
	}
	conversation, err := lifecycle.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "thread-cross-round", IdempotencyKey: "conv-cross-round",
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	prior, err := lifecycle.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "prior",
	})
	if err != nil {
		t.Fatalf("start prior interaction: %v", err)
	}
	priorRef := evidenceRef("claim:prior-total")
	priorRef.RefType = sessionvo.EvidenceRefClaim
	priorRef.SourceInteractionID = prior.ID
	priorRef.SourceRevisionID = "rev-prior"
	priorEvent := semanticEvent("evt-prior", "op-prior", 1)
	priorEvent.Owner, priorEvent.ConversationID, priorEvent.InteractionID = owner, conversation.ID, prior.ID
	priorEvent.EvidenceRefs = []sessionvo.EvidenceRef{priorRef}
	if _, err := ledger.Commit(context.Background(), priorEvent); err != nil {
		t.Fatalf("commit prior evidence: %v", err)
	}
	seedRevisionWithID(t, sessions, prior, "rev-prior", nil, []string{"evt-prior"})

	current, err := lifecycle.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "current",
	})
	if err != nil {
		t.Fatalf("start current interaction: %v", err)
	}
	currentClaim := claim("claim-comparison", sessionvo.SupportAdopted, "")
	currentClaim.Supports[0] = sessionvo.ClaimSupport{
		TargetRef: priorRef.Ref, TargetType: sessionvo.SupportClaim,
		SourceInteractionID: prior.ID, SourceRevisionID: "rev-prior",
		Version: priorRef.Version, ContentHash: priorRef.ContentHash,
		Role: "comparison_baseline", Status: sessionvo.SupportAdopted,
	}
	currentClaim.RequiredSupportRoles = []string{"comparison_baseline"}
	secondSupport := currentClaim.Supports[0]
	secondSupport.Role = "comparison_detail"
	currentClaim.Supports = append(currentClaim.Supports, secondSupport)
	currentClaim.RequiredSupportRoles = append(currentClaim.RequiredSupportRoles, secondSupport.Role)
	currentEvent := semanticEvent("evt-current", "op-current", 1)
	currentEvent.Owner, currentEvent.ConversationID, currentEvent.InteractionID = owner, conversation.ID, current.ID
	currentEvent.Claims = []sessionvo.Claim{currentClaim}
	if _, err := ledger.Commit(context.Background(), currentEvent); err != nil {
		t.Fatalf("commit current claim: %v", err)
	}
	seedRevisionWithID(t, sessions, current, "rev-current", []string{"claim-comparison"}, []string{"evt-current"})

	countedLedger := &countingLedger{Store: ledger, reads: make(map[string]int)}
	view, err := assemblysvc.NewQueryService(sessions, countedLedger).GetInteraction(context.Background(), owner, current.ID)
	if err != nil {
		t.Fatalf("query cross-round support: %v", err)
	}
	if view.Assembly.Completeness != sessionvo.EvidenceComplete || len(view.Assembly.Claims) != 1 ||
		len(view.Assembly.Claims[0].AdoptedSupports) != 2 {
		t.Fatalf("valid earlier immutable support was not assembled: %#v", view.Assembly)
	}
	if countedLedger.reads[prior.ID] != 1 {
		t.Fatalf("same source revision was loaded %d times, want once", countedLedger.reads[prior.ID])
	}

	currentClaim.Supports[0].SourceRevisionID = "rev-does-not-exist"
	currentEvent.EventID = "evt-invalid-cross-round"
	currentEvent.ProducerSequence = 2
	currentEvent.Claims = []sessionvo.Claim{currentClaim}
	currentEvent.Envelope = []byte(`{"revision":"invalid"}`)
	currentEvent.PayloadHash = ledgervo.CanonicalPayloadHash(currentEvent.Envelope)
	if _, err := ledger.Commit(context.Background(), currentEvent); err != nil {
		t.Fatalf("commit invalid cross-round candidate: %v", err)
	}
	seedRevisionWithID(t, sessions, current, "rev-current-2", []string{"claim-comparison"}, []string{"evt-invalid-cross-round"})
	invalid, err := assemblysvc.NewQueryService(sessions, countedLedger).GetInteraction(context.Background(), owner, current.ID)
	if err != nil {
		t.Fatalf("query invalid cross-round support: %v", err)
	}
	if invalid.Assembly.Completeness != sessionvo.EvidencePartial {
		t.Fatalf("unknown source revision must remain unresolved: %#v", invalid.Assembly)
	}
}

func queryFixture(t *testing.T) (*sessionstore.Store, *ledgerstore.Store, sessionvo.Owner, sessionvo.Interaction) {
	t.Helper()
	sessions := sessionstore.New()
	lifecycle := sessionsvc.New(sessions, sessionsvc.Options{})
	ledger := ledgerstore.New()
	owner := sessionvo.Owner{
		TenantID: "tenant-1", BusinessDomainID: "domain-1", ApplicationPrincipalID: "app-1",
		EffectiveSubjectType: sessionvo.SubjectService, EffectiveSubjectID: "agent-1",
	}
	conversation, err := lifecycle.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "thread-projection", IdempotencyKey: "conv-projection",
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	interaction, err := lifecycle.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "int-projection",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	event := semanticEvent("evt-projection", "op-query", 1)
	event.Owner, event.ConversationID, event.InteractionID = owner, conversation.ID, interaction.ID
	objectRef := sessionvo.BusinessRef{
		RefType: sessionvo.BusinessRefObjectType, RefID: "object:supplychain:forecast",
		BusinessDomainID: "domain-1", Version: "2026.07",
	}
	propertyRef := sessionvo.BusinessRef{
		RefType: sessionvo.BusinessRefProperty, RefID: "property:supplychain:forecast:qty",
		BusinessDomainID: "domain-1", Version: "2026.07",
	}
	event.BusinessRefs = []sessionvo.BusinessRef{objectRef, propertyRef}
	event.OperationBusinessEdges = []sessionvo.OperationBusinessEdge{
		{OperationID: "op-query", BusinessRef: objectRef, Role: sessionvo.OperationRoleRead, ObservedAt: event.ObservedAt},
		{OperationID: "op-query", BusinessRef: propertyRef, Role: sessionvo.OperationRoleAggregate, ObservedAt: event.ObservedAt},
	}
	if _, err := ledger.Commit(context.Background(), event); err != nil {
		t.Fatalf("commit semantic event: %v", err)
	}
	return sessions, ledger, owner, interaction
}

func seedRevision(t *testing.T, store *sessionstore.Store, interaction sessionvo.Interaction, claims, eventIDs []string) {
	seedRevisionWithID(t, store, interaction, "rev-1", claims, eventIDs)
}

func seedRevisionWithID(t *testing.T, store *sessionstore.Store, interaction sessionvo.Interaction, revisionID string, claims, eventIDs []string) {
	t.Helper()
	err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		current, _ := tx.FindInteraction(interaction.ID)
		current.ExecutionStatus = sessionvo.InteractionCompleted
		current.EvidenceStatus = sessionvo.EvidenceComplete
		current.ClosureManifest = &sessionvo.ClosureManifest{Version: "1", Claims: claims, CompletionReason: "answer_returned"}
		tx.SaveInteraction(current)
		tx.SaveAssemblyRevision(sessionvo.AssemblyRevision{
			ID: revisionID, RevisionNo: uint64(len(tx.ListAssemblyRevisions(interaction.ID)) + 1), InteractionID: interaction.ID,
			CompletionManifestVersion: "1", IncludedEventIDs: eventIDs,
			ArtifactManifestHash: "sha256:manifest", Completeness: sessionvo.EvidenceComplete,
			Trigger: "completion", CreatedAt: time.Now().UTC(),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed revision: %v", err)
	}
}
