package assemblysvc_test

import (
	"context"
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
}

func (r *queryResolver) ResolveBusinessRefs(_ context.Context, request ibusinessresolver.ResolveRequest) ([]ibusinessresolver.Resolution, error) {
	r.requests = append(r.requests, request)
	return r.resolutions, nil
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

func TestQueryProjectsAuthorizedBusinessNamesWithoutChangingAssemblyCompleteness(t *testing.T) {
	t.Parallel()

	sessions, ledger, owner, interaction := queryFixture(t)
	resolver := &queryResolver{resolutions: []ibusinessresolver.Resolution{
		{
			RefID: "object:supplychain:forecast", Visibility: "visible",
			Display: &evidencevo.BusinessDisplay{
				Name: "需求预测单", BusinessPath: []string{"HD供应链业务知识网络_v3", "需求预测单"},
				ResolutionStatus: "resolved", SourceVersion: "main",
			},
		},
		{RefID: "property:supplychain:forecast:qty", Visibility: "unauthorized"},
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
	if len(view.Assembly.BusinessRefs) != 1 {
		t.Fatalf("unauthorized ref count leaked or visible ref missing: %#v", view.Assembly.BusinessRefs)
	}
	projected := view.Assembly.BusinessRefs[0]
	if projected.Display == nil || projected.Display.Name != "需求预测单" ||
		projected.TechnicalRef.Version != "2026.07" || projected.DisclosureStatus != "visible" {
		t.Fatalf("authorized business display or technical ref missing: %#v", projected)
	}
	if len(view.Assembly.OperationBusinessEdges) != 1 ||
		view.Assembly.OperationBusinessEdges[0].BusinessRef.TechnicalRef.RefID != "object:supplychain:forecast" {
		t.Fatalf("operation edge was not projected with its authorized business ref: %#v", view.Assembly.OperationBusinessEdges)
	}
	if len(resolver.requests) != 1 || resolver.requests[0].Scope.Authorization != "Bearer current-user-token" {
		t.Fatalf("resolver did not receive current authorization scope: %#v", resolver.requests)
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
	currentEvent := semanticEvent("evt-current", "op-current", 1)
	currentEvent.Owner, currentEvent.ConversationID, currentEvent.InteractionID = owner, conversation.ID, current.ID
	currentEvent.Claims = []sessionvo.Claim{currentClaim}
	if _, err := ledger.Commit(context.Background(), currentEvent); err != nil {
		t.Fatalf("commit current claim: %v", err)
	}
	seedRevisionWithID(t, sessions, current, "rev-current", []string{"claim-comparison"}, []string{"evt-current"})

	view, err := assemblysvc.NewQueryService(sessions, ledger).GetInteraction(context.Background(), owner, current.ID)
	if err != nil {
		t.Fatalf("query cross-round support: %v", err)
	}
	if view.Assembly.Completeness != sessionvo.EvidenceComplete || len(view.Assembly.Claims) != 1 ||
		len(view.Assembly.Claims[0].AdoptedSupports) != 1 {
		t.Fatalf("valid earlier immutable support was not assembled: %#v", view.Assembly)
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
	invalid, err := assemblysvc.NewQueryService(sessions, ledger).GetInteraction(context.Background(), owner, current.ID)
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
