// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knactionrecall business knowledge network action recall business logic.
// file: index.go
package knactionrecall

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type knActionRecallServiceImpl struct {
	logger              interfaces.Logger
	config              *config.Config
	ontologyQuery       interfaces.DrivenOntologyQuery
	operatorIntegration interfaces.DrivenOperatorIntegration
}

var (
	karOnce               sync.Once
	knActionRecallService interfaces.IKnActionRecallService
)

// NewKnActionRecallService creates a business knowledge network action recall service instance.
func NewKnActionRecallService() interfaces.IKnActionRecallService {
	karOnce.Do(func() {
		configLoader := config.NewConfigLoader()
		knActionRecallService = &knActionRecallServiceImpl{
			logger:              configLoader.GetLogger(),
			config:              configLoader,
			ontologyQuery:       drivenadapters.NewOntologyQueryAccess(),
			operatorIntegration: drivenadapters.NewOperatorIntegrationClient(),
		}
	})
	return knActionRecallService
}
