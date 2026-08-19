// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knquerysubgraph business knowledge network subgraph query business logic.
// file: index.go
package knquerysubgraph

import (
	"context"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// KnQuerySubgraphService subgraph query service.
type KnQuerySubgraphService interface {
	QueryInstanceSubgraph(ctx context.Context, req *interfaces.QueryInstanceSubgraphReq) (resp *interfaces.QueryInstanceSubgraphResp, err error)
	ExploreSubgraph(ctx context.Context, req *interfaces.ExploreSubgraphReq) (resp *interfaces.ExploreSubgraphResp, err error)
}

type knQuerySubgraphService struct {
	Logger        interfaces.Logger
	OntologyQuery interfaces.DrivenOntologyQuery
}

var (
	kqsServiceOnce sync.Once
	kqsService     KnQuerySubgraphService
)

// NewKnQuerySubgraphService New KnQuerySubgraphService.
func NewKnQuerySubgraphService() KnQuerySubgraphService {
	kqsServiceOnce.Do(func() {
		conf := config.NewConfigLoader()
		kqsService = &knQuerySubgraphService{
			Logger:        conf.GetLogger(),
			OntologyQuery: drivenadapters.NewOntologyQueryAccess(),
		}
	})
	return kqsService
}

// QueryInstanceSubgraph queries the object subgraph.
func (s *knQuerySubgraphService) QueryInstanceSubgraph(ctx context.Context, req *interfaces.QueryInstanceSubgraphReq) (resp *interfaces.QueryInstanceSubgraphResp, err error) {
	// Call the drivenadapters layer to query the subgraph.
	resp, err = s.OntologyQuery.QueryInstanceSubgraph(ctx, req)
	if err == nil {
		bkntrace.EmitQueryInstanceSubgraphEvents(ctx, s.Logger, req, resp)
	}
	return
}

// ExploreSubgraph runs source-based exploratory subgraph queries.
func (s *knQuerySubgraphService) ExploreSubgraph(ctx context.Context, req *interfaces.ExploreSubgraphReq) (resp *interfaces.ExploreSubgraphResp, err error) {
	resp, err = s.OntologyQuery.ExploreSubgraph(ctx, req)
	if err == nil {
		bkntrace.EmitExploreSubgraphEvents(ctx, s.Logger, req, resp)
	}
	return
}
