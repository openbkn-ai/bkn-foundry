// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

//go:generate mockgen -source=interface.go -destination=../mocks/interface.go -package=mocks
import "context"

// App Application interface
type App interface {
	Start() error
	Stop(context.Context)
}

type ResourceDeployType string

func (r ResourceDeployType) String() string {
	return string(r)
}

// IKnQuerySubgraphService Subgraph query service interface
type IKnQuerySubgraphService interface {
	// QueryInstanceSubgraph Query object subgraph along caller-supplied type paths
	QueryInstanceSubgraph(ctx context.Context, req *QueryInstanceSubgraphReq) (resp *QueryInstanceSubgraphResp, err error)
	// ExploreSubgraph Explore an object subgraph from a source object type
	ExploreSubgraph(ctx context.Context, req *ExploreSubgraphReq) (resp *ExploreSubgraphResp, err error)
}

// IKnSearchService kn_search service interface
type IKnSearchService interface {
	// KnSearch Knowledge network retrieval
	KnSearch(ctx context.Context, req *KnSearchReq) (resp *KnSearchResp, err error)
	// SearchSchema Unified schema search with normalization and output filtering
	SearchSchema(ctx context.Context, req *SearchSchemaReq) (resp *SearchSchemaResp, err error)
	// SearchInstance Natural-language instance recall; returns instances only
	SearchInstance(ctx context.Context, req *SearchInstanceReq) (resp *SearchInstanceResp, err error)
}
