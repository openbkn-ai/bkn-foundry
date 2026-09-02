// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_authorization

import (
	"context"
	"os"
	"strings"

	"ontology-query/interfaces"
)

const queryDataPEPEnabledEnv = "QUERY_DATA_PEP_ENABLED"

// QueryDataPEPEnabled reports whether ontology query_data authorization is
// enabled. It defaults to false until existing policies and ResourceParent data
// have been migrated and cross-service authorization has been validated.
func QueryDataPEPEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(queryDataPEPEnabledEnv)))
	return value == "true" || value == "1"
}

// noopQueryAuthorizationService preserves the legacy query behavior while the
// query_data PEP is disabled. In particular, it does not resolve published
// models, restrict branches, call BKN Safe, or filter subgraph candidates.
type noopQueryAuthorizationService struct{}

func (s *noopQueryAuthorizationService) AuthorizeObjectTypeQuery(context.Context, string, string, string) error {
	return nil
}

func (s *noopQueryAuthorizationService) AuthorizeActionTypeQuery(context.Context, string, string, string) error {
	return nil
}

func (s *noopQueryAuthorizationService) AuthorizeMetricQuery(context.Context, string, string, string) error {
	return nil
}

func (s *noopQueryAuthorizationService) AuthorizeMetricDryRun(context.Context, string, string,
	*interfaces.MetricDefinition) error {
	return nil
}

func (s *noopQueryAuthorizationService) AuthorizeSubgraphBySource(context.Context,
	*interfaces.SubGraphQueryBaseOnSource) error {
	return nil
}

func (s *noopQueryAuthorizationService) AuthorizeSubgraphByTypePath(context.Context,
	*interfaces.SubGraphQueryBaseOnTypePath) error {
	return nil
}

func (s *noopQueryAuthorizationService) AuthorizeSubgraphByObjects(context.Context,
	*interfaces.SubGraphQueryBaseOnObjects) error {
	return nil
}
