// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knquerysubgraph 业务知识网络子图查询业务逻辑
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

// KnQuerySubgraphService 子图查询服务
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

// NewKnQuerySubgraphService 新建 KnQuerySubgraphService
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

// QueryInstanceSubgraph 查询对象子图
func (s *knQuerySubgraphService) QueryInstanceSubgraph(ctx context.Context, req *interfaces.QueryInstanceSubgraphReq) (resp *interfaces.QueryInstanceSubgraphResp, err error) {
	// 调用 drivenadapters 层查询子图
	resp, err = s.OntologyQuery.QueryInstanceSubgraph(ctx, req)
	if err == nil {
		bkntrace.EmitQueryInstanceSubgraphEvents(ctx, s.Logger, req, resp)
	}
	return
}

// ExploreSubgraph 起点探索式子图查询
func (s *knQuerySubgraphService) ExploreSubgraph(ctx context.Context, req *interfaces.ExploreSubgraphReq) (resp *interfaces.ExploreSubgraphResp, err error) {
	resp, err = s.OntologyQuery.ExploreSubgraph(ctx, req)
	if err == nil {
		bkntrace.EmitExploreSubgraphEvents(ctx, s.Logger, req, resp)
	}
	return
}
