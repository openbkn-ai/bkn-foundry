// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch provides business logic for knowledge network search operations.
package knsearch

import (
	"context"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// localSearchImpl local search implementation body.
type localSearchImpl struct {
	logger        interfaces.Logger
	config        *config.Config
	bknBackend    interfaces.BknBackendAccess
	ontologyQuery interfaces.DrivenOntologyQuery
	rerankClient  interfaces.DrivenMFModelAPIClient
}

var (
	localSearchOnce    sync.Once
	localSearchService interfaces.IKnSearchLocalService
)

// NewLocalSearchService creates a knowledge network retrieval local service instance.
func NewLocalSearchService() interfaces.IKnSearchLocalService {
	localSearchOnce.Do(func() {
		configLoader := config.NewConfigLoader()
		localSearchService = &localSearchImpl{
			logger:        configLoader.GetLogger(),
			config:        configLoader,
			bknBackend:    drivenadapters.NewBknBackendAccess(),
			ontologyQuery: drivenadapters.NewOntologyQueryAccess(),
			rerankClient:  drivenadapters.NewMFModelAPIClient(),
		}
	})
	return localSearchService
}

// KnSearchService kn_search service
type KnSearchService interface {
	KnSearch(ctx context.Context, req *interfaces.KnSearchReq) (resp *interfaces.KnSearchResp, err error)
	SearchSchema(ctx context.Context, req *interfaces.SearchSchemaReq) (resp *interfaces.SearchSchemaResp, err error)
	SearchInstance(ctx context.Context, req *interfaces.SearchInstanceReq) (resp *interfaces.SearchInstanceResp, err error)
}

type knSearchService struct {
	Logger      interfaces.Logger
	LocalSearch interfaces.IKnSearchLocalService
}

var (
	ksServiceOnce sync.Once
	ksService     KnSearchService
)

// NewKnSearchService creates new KnSearchService
func NewKnSearchService() KnSearchService {
	ksServiceOnce.Do(func() {
		conf := config.NewConfigLoader()
		logger := conf.GetLogger()

		ksService = &knSearchService{
			Logger:      logger,
			LocalSearch: NewLocalSearchService(),
		}
	})
	return ksService
}

// KnSearch Knowledge network retrieval
func (s *knSearchService) KnSearch(ctx context.Context, req *interfaces.KnSearchReq) (resp *interfaces.KnSearchResp, err error) {
	// Convert kn_id to kn_ids array (internal use, not exposed)
	knIDs := []*interfaces.KnDataSourceConfig{
		{
			KnowledgeNetworkID: req.KnID,
		},
	}
	req.SetKnIDs(knIDs)

	// kn_search has been implemented locally and the remote data-retrieval bypass branch has been removed.
	s.Logger.WithContext(ctx).Info("[KnSearch] Using local search")
	localReq := KnSearchReqToLocal(req)
	localResp, err := s.LocalSearch.Search(ctx, localReq)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("[KnSearch] Local search failed: %v", err)
		return nil, err
	}
	return KnSearchLocalResponseToResp(localResp), nil
}
