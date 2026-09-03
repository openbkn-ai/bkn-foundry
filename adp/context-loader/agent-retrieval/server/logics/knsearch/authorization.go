// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knsearch

import (
	"context"
	"net/http"

	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func (s *localSearchImpl) knPEPEnabled() bool {
	return s != nil && s.config != nil && s.config.Auth.ContextLoaderKNPEPEnabled
}

func (s *localSearchImpl) filterAuthorizedObjectTypes(ctx context.Context, knID string,
	objectTypes []*interfaces.KnSearchObjectType,
) ([]*interfaces.KnSearchObjectType, error) {
	if s == nil || s.authorizer == nil {
		return nil, infraerrors.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			"query authorization is not configured")
	}
	ids := make([]string, 0, len(objectTypes))
	for _, objectType := range objectTypes {
		if objectType != nil {
			ids = append(ids, objectType.ConceptID)
		}
	}
	allowedIDs, err := s.authorizer.FilterObjectTypeIDs(ctx, knID, ids)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	filtered := make([]*interfaces.KnSearchObjectType, 0, len(allowed))
	for _, objectType := range objectTypes {
		if objectType == nil {
			continue
		}
		if _, ok := allowed[objectType.ConceptID]; ok {
			filtered = append(filtered, objectType)
		}
	}
	return filtered, nil
}

// fetchAuthorizedSampleData keeps schema visibility and data-query permission
// independent. Object types denied query_data remain in the schema result, but
// are removed from the candidate set before any ontology-query request is sent.
func (s *localSearchImpl) fetchAuthorizedSampleData(ctx context.Context, knID string,
	objectTypes []*interfaces.KnSearchObjectType, brief bool,
) error {
	targets := objectTypes
	if s.knPEPEnabled() {
		var err error
		targets, err = s.filterAuthorizedObjectTypes(ctx, knID, objectTypes)
		if err != nil {
			return err
		}
	}
	if len(targets) == 0 {
		return nil
	}
	return s.fetchSampleData(ctx, knID, targets, brief)
}

func isAuthorizationError(err error) bool {
	return infraerrors.IsAuthorizationError(err)
}

func protectedDependencyUnavailable(ctx context.Context, dependency string) error {
	return infraerrors.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
		dependency+" returned an incomplete protected response")
}
