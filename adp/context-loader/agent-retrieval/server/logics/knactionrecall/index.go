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
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/permission"
)

type knActionRecallServiceImpl struct {
	logger              interfaces.Logger
	config              *config.Config
	ontologyQuery       interfaces.DrivenOntologyQuery
	operatorIntegration interfaces.DrivenOperatorIntegration

	// The three below serve only the fallback for a caller with no grant on the bound tool: its
	// definition is read through the knowledge network's proxy once the caller's own view on the
	// action type is confirmed (#1548). Any of them missing refuses that read.
	actionAuthz   interfaces.ActionTypeViewAuthorizer
	proxyResolver interfaces.KNProxyResolver
	proxyReader   interfaces.KNProxyDefinitionReader
}

var (
	karOnce               sync.Once
	knActionRecallService interfaces.IKnActionRecallService
)

// NewKnActionRecallService creates a business knowledge network action recall service instance.
func NewKnActionRecallService() interfaces.IKnActionRecallService {
	karOnce.Do(func() {
		configLoader := config.NewConfigLoader()
		operatorIntegration := drivenadapters.NewOperatorIntegrationClient()
		proxyReader, _ := operatorIntegration.(interfaces.KNProxyDefinitionReader)
		proxyResolver, _ := drivenadapters.NewBknBackendAccess().(interfaces.KNProxyResolver)
		knActionRecallService = &knActionRecallServiceImpl{
			logger:              configLoader.GetLogger(),
			config:              configLoader,
			ontologyQuery:       drivenadapters.NewOntologyQueryAccess(),
			operatorIntegration: operatorIntegration,
			actionAuthz:         permission.NewActionTypeViewAuthorizer(configLoader),
			proxyResolver:       proxyResolver,
			proxyReader:         proxyReader,
		}
	})
	return knActionRecallService
}
