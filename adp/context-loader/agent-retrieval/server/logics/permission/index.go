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
			CandidateOperations:  []string{interfaces.PermissionOperationQueryData},
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
			if !contains(result.Operations, interfaces.PermissionOperationQueryData) {
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
