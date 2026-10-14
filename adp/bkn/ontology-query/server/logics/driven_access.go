// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

var (
	AA  interfaces.AuthAccess
	AOA interfaces.AgentOperatorAccess
	MFA interfaces.ModelFactoryAccess
	OMA interfaces.OntologyManagerAccess
	OSA interfaces.OpenSearchAccess
	VBA interfaces.VegaBackendAccess
	PCR interfaces.ProxyContextResolver

	objectMetricQueryMu      sync.RWMutex
	objectMetricQueryService interfaces.ObjectMetricQueryServiceV1
)

func SetAuthAccess(aa interfaces.AuthAccess) {
	AA = aa
}

func SetAgentOperatorAccess(aoa interfaces.AgentOperatorAccess) {
	AOA = aoa
}

func SetModelFactoryAccess(mfa interfaces.ModelFactoryAccess) {
	MFA = mfa
}

func SetOntologyManagerAccess(ota interfaces.OntologyManagerAccess) {
	OMA = ota
}

func SetOpenSearchAccess(osa interfaces.OpenSearchAccess) {
	OSA = osa
}

func SetVegaBackendAccess(v interfaces.VegaBackendAccess) {
	VBA = v
}

func SetProxyContextResolver(resolver interfaces.ProxyContextResolver) {
	PCR = resolver
}

// SetObjectMetricQueryService installs the paid object-metric runtime during
// Boot -> Setup -> Run assembly. Keeping this socket in core allows object
// logical properties to resolve the optional provider without importing EE.
func SetObjectMetricQueryService(service interfaces.ObjectMetricQueryServiceV1) {
	objectMetricQueryMu.Lock()
	defer objectMetricQueryMu.Unlock()
	objectMetricQueryService = service
}

// ObjectMetricQueryService returns the currently assembled optional runtime.
func ObjectMetricQueryService() interfaces.ObjectMetricQueryServiceV1 {
	objectMetricQueryMu.RLock()
	defer objectMetricQueryMu.RUnlock()
	return objectMetricQueryService
}
