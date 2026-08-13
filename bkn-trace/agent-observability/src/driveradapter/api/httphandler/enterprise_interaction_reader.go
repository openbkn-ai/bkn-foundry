package httphandler

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/extension/enterpriseroute"
)

type enterpriseInteractionSummarySource interface {
	GetInteractionSummary(context.Context, string, evidencevo.QueryScope) (evidencevo.InteractionSummary, bool, error)
	ListConversations(context.Context, evidencevo.SummaryQueryOptions) (evidencevo.ConversationSummaryPage, error)
	ListInteractions(context.Context, evidencevo.SummaryQueryOptions) (evidencevo.InteractionSummaryPage, error)
}

func (r enterpriseInteractionFactsReader) ListConversations(
	ctx context.Context,
	query enterpriseroute.ListQuery,
) (evidencevo.ConversationSummaryPage, error) {
	options, ok := trustedListOptions(ctx, query)
	if !ok {
		return evidencevo.ConversationSummaryPage{}, nil
	}
	return r.summaries.ListConversations(ctx, options)
}

func (r enterpriseInteractionFactsReader) ListInteractions(
	ctx context.Context,
	query enterpriseroute.ListQuery,
) (evidencevo.InteractionSummaryPage, error) {
	options, ok := trustedListOptions(ctx, query)
	if !ok {
		return evidencevo.InteractionSummaryPage{}, nil
	}
	return r.summaries.ListInteractions(ctx, options)
}

func trustedListOptions(ctx context.Context, query enterpriseroute.ListQuery) (evidencevo.SummaryQueryOptions, bool) {
	scope, ok := trustedQueryScopeFromContext(ctx)
	if !ok {
		return evidencevo.SummaryQueryOptions{}, false
	}
	scope.View = evidencevo.AccessViewTechnical
	return evidencevo.SummaryQueryOptions{
		ConversationID: query.ConversationID, Page: query.Page, Limit: query.PageSize, Keyword: query.Keyword, Status: query.Status,
		AgentOrApp: query.AgentOrApp, ExcludeAgentOrApp: query.ExcludeAgentOrApp,
		BusinessDomain:   query.BusinessDomain,
		KnowledgeNetwork: query.KnowledgeNetwork, EvidenceCompleteness: query.EvidenceCompleteness,
		Scope: scope,
	}, true
}

type enterpriseInteractionOperationSource interface {
	ListOperationExecutionsByInteractionIDScoped(context.Context, evidencevo.QueryScope, string) ([]sessionvo.OperationExecution, error)
}

type enterpriseInteractionFactsReader struct {
	summaries  enterpriseInteractionSummarySource
	operations enterpriseInteractionOperationSource
}

// NewEnterpriseInteractionFactsReader exposes only the current caller's
// already-authorized interaction summary and ordered Operation call facts to a
// mounted EE route. It never exposes Core stores to the EE overlay.
func NewEnterpriseInteractionFactsReader(
	summaries enterpriseInteractionSummarySource,
	operations enterpriseInteractionOperationSource,
) enterpriseroute.Reader {
	return enterpriseInteractionFactsReader{summaries: summaries, operations: operations}
}

func (r enterpriseInteractionFactsReader) ReadInteraction(
	ctx context.Context,
	interactionID string,
) (enterpriseroute.InteractionFacts, bool, error) {
	scope, ok := trustedQueryScopeFromContext(ctx)
	if !ok {
		return enterpriseroute.InteractionFacts{}, false, nil
	}
	scope.View = evidencevo.AccessViewTechnical
	summary, found, err := r.summaries.GetInteractionSummary(ctx, interactionID, scope)
	if err != nil || !found {
		return enterpriseroute.InteractionFacts{}, found, err
	}
	operations, err := r.operations.ListOperationExecutionsByInteractionIDScoped(ctx, scope, interactionID)
	if err != nil {
		return enterpriseroute.InteractionFacts{}, false, err
	}
	sourceRead := enterpriseroute.SourceReadContext{
		Authorization: scope.Authorization, TenantID: scope.TenantID,
		BusinessDomain: scope.BusinessDomain, AccountID: scope.AccountID, AccountType: scope.AccountType,
		EffectiveSubjectID: scope.AccountID, EffectiveSubjectType: trustedSubjectType(scope.AccountType),
	}
	if scope.AccessProfile != nil {
		sourceRead.ApplicationPrincipalID = scope.AccessProfile.ApplicationPrincipalID
		sourceRead.EffectiveSubjectID = scope.AccessProfile.EffectiveSubjectID
		sourceRead.DelegationID = scope.AccessProfile.DelegationID
	}
	return enterpriseroute.InteractionFacts{
		Summary: summary, Operations: operations,
		SourceRead: sourceRead,
	}, true, nil
}

func trustedSubjectType(accountType string) string {
	if accountType == "app" || accountType == "service" {
		return "service"
	}
	return "user"
}
