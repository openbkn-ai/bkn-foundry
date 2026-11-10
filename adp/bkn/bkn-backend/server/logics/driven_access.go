// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

var (
	DB             *sql.DB
	AA             interfaces.AuthAccess
	AOA            interfaces.AgentOperatorAccess
	AEA            interfaces.ActionExecutionAccess
	ASA            interfaces.ActionScheduleAccess
	ATA            interfaces.ActionTypeAccess
	CBA            interfaces.CapabilityBindingAccess
	CGA            interfaces.ConceptGroupAccess
	KBA            interfaces.KnowledgeBaseAccess
	KNA            interfaces.KNAccess
	KNVA           interfaces.KNVersionAccess
	BC             interfaces.BranchCopier
	KECA           interfaces.KnEventCleanupAccess
	KPA            interfaces.KNProxyAccess
	KPOA           interfaces.KNProxyOutboxAccess
	OMA            interfaces.ObjectMetricAccessV1
	OMVP           interfaces.ObjectMetricVersionPublisher
	MPA            interfaces.ManagedProxyAccess
	MFA            interfaces.ModelFactoryAccess
	OSA            interfaces.OpenSearchAccess
	OTA            interfaces.ObjectTypeAccess
	PA             interfaces.PermissionAccess
	RTA            interfaces.RelationTypeAccess
	RiskTypeAccess interfaces.RiskTypeAccess
	UMA            interfaces.UserMgmtAccess
	VBA            interfaces.VegaBackendAccess
	VTA            interfaces.ValueTypeAccess
)

func SetDB(db *sql.DB) {
	DB = db
}

func SetAuthAccess(aa interfaces.AuthAccess) {
	AA = aa
}

func SetActionScheduleAccess(asa interfaces.ActionScheduleAccess) {
	ASA = asa
}

func SetActionExecutionAccess(aea interfaces.ActionExecutionAccess) {
	AEA = aea
}

func SetActionTypeAccess(ata interfaces.ActionTypeAccess) {
	ATA = ata
}

func SetCapabilityBindingAccess(cba interfaces.CapabilityBindingAccess) {
	CBA = cba
}

func SetConceptGroupAccess(cga interfaces.ConceptGroupAccess) {
	CGA = cga
}

func SetKNAccess(kna interfaces.KNAccess) {
	KNA = kna
}

func SetKNVersionAccess(knva interfaces.KNVersionAccess, bc interfaces.BranchCopier) {
	KNVA = knva
	BC = bc
}

func SetKNProxyAccess(kpa interfaces.KNProxyAccess) {
	KPA = kpa
}

func SetKNProxyOutboxAccess(kpoa interfaces.KNProxyOutboxAccess) {
	KPOA = kpoa
}

func SetManagedProxyAccess(mpa interfaces.ManagedProxyAccess) {
	MPA = mpa
}

func SetObjectMetricAccess(oma interfaces.ObjectMetricAccessV1) {
	OMA = oma
}

// SetObjectMetricVersionPublisher installs the paid image's publication of metrics with the
// network. Without it a network publish leaves metrics alone.
func SetObjectMetricVersionPublisher(p interfaces.ObjectMetricVersionPublisher) {
	OMVP = p
}

// SetKnEventCleanupAccess installs the paid image's cleanup of a network's
// events, run in the transaction that deletes the network.
func SetKnEventCleanupAccess(keca interfaces.KnEventCleanupAccess) {
	KECA = keca
}

func SetModelFactoryAccess(mfa interfaces.ModelFactoryAccess) {
	MFA = mfa
}

func SetOpenSearchAccess(osa interfaces.OpenSearchAccess) {
	OSA = osa
}

func SetObjectTypeAccess(ota interfaces.ObjectTypeAccess) {
	OTA = ota
}

func SetPermissionAccess(pa interfaces.PermissionAccess) {
	PA = pa
}

func SetRelationTypeAccess(rta interfaces.RelationTypeAccess) {
	RTA = rta
}

func SetRiskTypeAccess(rta interfaces.RiskTypeAccess) {
	RiskTypeAccess = rta
}

func SetAgentOperatorAccess(aoa interfaces.AgentOperatorAccess) {
	AOA = aoa
}

// SetKnowledgeBaseAccess sets the kb-api identity reader that knowledge_base capability
// bindings use; it answers ErrKnowledgeBaseUnavailable where kb-api is not deployed.
func SetKnowledgeBaseAccess(kba interfaces.KnowledgeBaseAccess) {
	KBA = kba
}

func SetUserMgmtAccess(uma interfaces.UserMgmtAccess) {
	UMA = uma
}

func SetVegaBackendAccess(vba interfaces.VegaBackendAccess) {
	VBA = vba
}

func SetValueTypeAccess(vta interfaces.ValueTypeAccess) {
	VTA = vta
}
