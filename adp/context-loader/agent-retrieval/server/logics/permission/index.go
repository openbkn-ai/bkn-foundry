// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package permission implements context-loader's narrow KN query-candidate
// authorization boundary.
package permission

import (
	"context"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const defaultResourceFilterChunkSize = 200

type queryCandidateAuthorizer struct {
	access    interfaces.PermissionAccess
	chunkSize int
}

// NewQueryCandidateAuthorizer wires the production Safe adapter.
func NewQueryCandidateAuthorizer(conf *config.Config) interfaces.QueryCandidateAuthorizer {
	chunkSize := defaultResourceFilterChunkSize
	if conf != nil && conf.Auth.ResourceFilterChunkSize > 0 {
		chunkSize = conf.Auth.ResourceFilterChunkSize
	}
	return NewQueryCandidateAuthorizerWith(drivenadapters.NewPermissionAccess(conf), chunkSize)
}

// NewQueryCandidateAuthorizerWith allows focused tests to inject the outbound boundary.
func NewQueryCandidateAuthorizerWith(access interfaces.PermissionAccess, chunkSize int) interfaces.QueryCandidateAuthorizer {
	if chunkSize <= 0 {
		chunkSize = defaultResourceFilterChunkSize
	}
	return &queryCandidateAuthorizer{access: access, chunkSize: chunkSize}
}

func (a *queryCandidateAuthorizer) FilterObjectTypeIDs(ctx context.Context,
	knID string, candidateIDs []string,
) ([]string, error) {
	if len(candidateIDs) == 0 {
		return []string{}, nil
	}
	account, ok := trustedAccount(ctx)
	if !ok {
		return nil, infraerrors.DefaultHTTPError(ctx, http.StatusUnauthorized, "request subject is missing or invalid")
	}
	knID = strings.TrimSpace(knID)
	if !validAuthorizationID(knID) {
		return nil, infraerrors.DefaultHTTPError(ctx, http.StatusBadRequest, "invalid knowledge network id")
	}
	if a == nil || a.access == nil {
		return nil, permissionUnavailable(ctx)
	}

	normalized := make([]string, 0, len(candidateIDs))
	seen := make(map[string]struct{}, len(candidateIDs))
	for _, candidateID := range candidateIDs {
		candidateID = strings.TrimSpace(candidateID)
		if !validAuthorizationID(candidateID) {
			return nil, permissionUnavailable(ctx)
		}
		if _, exists := seen[candidateID]; exists {
			continue
		}
		seen[candidateID] = struct{}{}
		normalized = append(normalized, candidateID)
	}

	allowed := make(map[string]struct{}, len(normalized))
	for start := 0; start < len(normalized); start += a.chunkSize {
		end := start + a.chunkSize
		if end > len(normalized) {
			end = len(normalized)
		}
		resources := make([]interfaces.PermissionResource, 0, end-start)
		requested := make(map[string]struct{}, end-start)
		for _, candidateID := range normalized[start:end] {
			canonicalID := knID + "/" + candidateID
			resources = append(resources, interfaces.PermissionResource{
				Type: interfaces.PermissionResourceTypeObjectType,
				ID:   canonicalID,
			})
			requested[canonicalID] = struct{}{}
		}

		response, err := a.access.FilterResources(ctx, interfaces.PermissionFilterRequest{
			AccessorID:           account.AccountID,
			Resources:            resources,
			VisibilityOperations: []string{interfaces.PermissionOperationQueryData},
			IncludeOperations:    false,
		})
		if err != nil || response.Resources == nil {
			return nil, permissionUnavailable(ctx)
		}
		returned := make(map[string]struct{}, len(*response.Resources))
		for _, result := range *response.Resources {
			if result.ResourceType != interfaces.PermissionResourceTypeObjectType {
				return nil, permissionUnavailable(ctx)
			}
			if _, exists := requested[result.ResourceID]; !exists {
				return nil, permissionUnavailable(ctx)
			}
			if _, duplicate := returned[result.ResourceID]; duplicate {
				return nil, permissionUnavailable(ctx)
			}
			returned[result.ResourceID] = struct{}{}
			allowed[result.ResourceID] = struct{}{}
		}
	}

	result := make([]string, 0, len(allowed))
	for _, candidateID := range normalized {
		if _, ok := allowed[knID+"/"+candidateID]; ok {
			result = append(result, candidateID)
		}
	}
	return result, nil
}

func trustedAccount(ctx context.Context) (*interfaces.AccountAuthContext, bool) {
	if ctx == nil {
		return nil, false
	}
	account, ok := common.GetAccountAuthContextFromCtx(ctx)
	if !ok || account == nil || account.AccountID == "" ||
		strings.TrimSpace(account.AccountID) != account.AccountID || strings.ContainsAny(account.AccountID, " \t\r\n*") {
		return nil, false
	}
	switch account.AccountType {
	case interfaces.AccessorTypeUser, interfaces.AccessorTypeApp:
		return account, true
	default:
		return nil, false
	}
}

func validAuthorizationID(id string) bool {
	return id != "" && strings.TrimSpace(id) == id && !strings.ContainsAny(id, "/*")
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func permissionUnavailable(ctx context.Context) error {
	return infraerrors.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
		"query authorization is unavailable")
}

type knowledgeNetworkAuthorizer struct {
	access interfaces.PermissionAccess
}

// NewKnowledgeNetworkAuthorizer wires the production Safe adapter.
func NewKnowledgeNetworkAuthorizer(conf *config.Config) interfaces.KnowledgeNetworkAuthorizer {
	return NewKnowledgeNetworkAuthorizerWith(drivenadapters.NewPermissionAccess(conf))
}

// NewKnowledgeNetworkAuthorizerWith allows focused tests to inject the outbound boundary.
func NewKnowledgeNetworkAuthorizerWith(access interfaces.PermissionAccess) interfaces.KnowledgeNetworkAuthorizer {
	return &knowledgeNetworkAuthorizer{access: access}
}

// AuthorizeRead checks view_detail on the network itself.
//
// Fail-closed throughout: no subject, an unusable id, or an unavailable authorization service all
// refuse. An answer built without this check would be scoped only by the kn_id the caller typed.
func (a *knowledgeNetworkAuthorizer) AuthorizeRead(ctx context.Context, knID string) error {
	return a.authorize(ctx, knID, interfaces.PermissionOperationViewDetail)
}

func (a *knowledgeNetworkAuthorizer) AuthorizeExecute(ctx context.Context, knID string) error {
	return a.authorize(ctx, knID, interfaces.PermissionOperationExecute)
}

// NewActionTypeViewAuthorizer wires the production Safe adapter.
func NewActionTypeViewAuthorizer(conf *config.Config) interfaces.ActionTypeViewAuthorizer {
	return NewActionTypeViewAuthorizerWith(drivenadapters.NewPermissionAccess(conf))
}

// NewActionTypeViewAuthorizerWith allows focused tests to inject the outbound boundary.
func NewActionTypeViewAuthorizerWith(access interfaces.PermissionAccess) interfaces.ActionTypeViewAuthorizer {
	return &knowledgeNetworkAuthorizer{access: access}
}

// AuthorizeActionTypeView checks view_detail on one action type, the same canonical child
// resource bkn-backend checks before it returns the action type's detail. A grant on the whole
// network reaches it through Safe's resource hierarchy.
func (a *knowledgeNetworkAuthorizer) AuthorizeActionTypeView(ctx context.Context, knID, atID string) error {
	account, ok := trustedAccount(ctx)
	if !ok {
		return infraerrors.DefaultHTTPError(ctx, http.StatusUnauthorized, "request subject is missing or invalid")
	}
	knID, atID = strings.TrimSpace(knID), strings.TrimSpace(atID)
	if !validAuthorizationID(knID) || !validAuthorizationID(atID) {
		return infraerrors.DefaultHTTPError(ctx, http.StatusBadRequest, "invalid knowledge network or action type id")
	}
	return a.authorizeResource(ctx, account, interfaces.PermissionResource{
		Type: interfaces.PermissionResourceTypeActionType,
		ID:   knID + "/" + atID,
	}, interfaces.PermissionOperationViewDetail, "ActionTypeNotAuthorized")
}

func (a *knowledgeNetworkAuthorizer) authorize(ctx context.Context, knID, operation string) error {
	account, ok := trustedAccount(ctx)
	if !ok {
		return infraerrors.DefaultHTTPError(ctx, http.StatusUnauthorized, "request subject is missing or invalid")
	}
	knID = strings.TrimSpace(knID)
	if !validAuthorizationID(knID) {
		return infraerrors.DefaultHTTPError(ctx, http.StatusBadRequest, "invalid knowledge network id")
	}
	return a.authorizeResource(ctx, account, interfaces.PermissionResource{
		Type: interfaces.PermissionResourceTypeKnowledgeNetwork,
		ID:   knID,
	}, operation, "KnowledgeNetworkNotAuthorized")
}

func (a *knowledgeNetworkAuthorizer) authorizeResource(ctx context.Context, account *interfaces.AccountAuthContext,
	resource interfaces.PermissionResource, operation, deniedDetailKey string) error {
	if a == nil || a.access == nil {
		return permissionUnavailable(ctx)
	}

	response, err := a.access.CheckPermissions(ctx, interfaces.PermissionChecksRequest{
		AccessorID: account.AccountID,
		Checks: []interfaces.PermissionCheck{{
			Resource: resource, Operation: operation,
		}},
	})
	if err != nil || len(response.Results) != 1 {
		return permissionUnavailable(ctx)
	}
	result := response.Results[0]
	if result.ResourceType != resource.Type || result.ResourceID != resource.ID || result.Operation != operation {
		return permissionUnavailable(ctx)
	}
	if result.Allowed {
		return nil
	}
	return infraerrors.DefaultHTTPError(ctx, http.StatusForbidden,
		infraerrors.LocalizedDetail(ctx, deniedDetailKey))
}

// Refusal details for the network-scoped capability tools.
const (
	// CapabilityNetworkViewRequired explains a refused discovery or Skill read.
	CapabilityNetworkViewRequired = "CapabilityNetworkViewRequired"
	// CapabilityNetworkExecuteRequired explains a refused tool execution.
	CapabilityNetworkExecuteRequired = "CapabilityNetworkExecuteRequired"
)

// CapabilityScopeError restates a knowledge-network refusal for the tools that
// work on what a network has mounted (#1550).
//
// Those tools need the operation on the network itself: a caller who holds
// only child grants, or a standalone grant on one Skill, is refused, and the
// generic "not authorized to read this network" leaves them guessing which
// grant is missing. Only a 403 is restated; every other outcome, including an
// unavailable authorization service, keeps its own status and detail.
func CapabilityScopeError(ctx context.Context, err error, detailKey string) error {
	if status, ok := infraerrors.HTTPStatus(err); ok && status == http.StatusForbidden {
		return infraerrors.DefaultHTTPError(ctx, http.StatusForbidden, infraerrors.LocalizedDetail(ctx, detailKey))
	}
	return err
}
