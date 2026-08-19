// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knretrieval realizes unified retrieval based on business knowledge network.
// file: index.go
package knretrieval

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knrerank"
)

type knRetrievalServiceImpl struct {
	logger              interfaces.Logger
	ontologyQueryAccess interfaces.DrivenOntologyQuery
	bknBackendAccess    interfaces.BknBackendAccess
	knReranker          *knrerank.KnowledgeReranker
}

var (
	krOnce             sync.Once
	knRetrievalService interfaces.IKnRetrievalService
)

func NewKnRetrievalService() interfaces.IKnRetrievalService {
	krOnce.Do(func() {
		conf := config.NewConfigLoader()
		logger := conf.GetLogger()

		// Create a unified mf-model-api client (providing both LLM and Rerank capabilities)
		mfModelClient := drivenadapters.NewMFModelAPIClient()

		knRetrievalService = &knRetrievalServiceImpl{
			logger:              logger,
			ontologyQueryAccess: drivenadapters.NewOntologyQueryAccess(),
			bknBackendAccess:    drivenadapters.NewBknBackendAccess(),
			knReranker:          knrerank.NewKnowledgeReranker(mfModelClient, logger), // Singleton.
		}
	})
	return knRetrievalService
}
