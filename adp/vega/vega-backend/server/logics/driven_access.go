// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package logics contains business logic implementations.
package logics

import (
	"database/sql"

	"vega-backend/interfaces"
)

var (
	DB   *sql.DB
	AA   interfaces.AuthAccess
	AQA  interfaces.AsynqAccess
	BTA  interfaces.BuildTaskAccess
	CA   interfaces.CatalogAccess
	CTA  interfaces.ConnectorTypeAccess
	DSA  interfaces.DiscoverScheduleAccess
	DTA  interfaces.DiscoverTaskAccess
	KA   interfaces.KafkaAccess
	MFA  interfaces.ModelFactoryAccess
	PA   interfaces.PermissionAccess
	RA   interfaces.ResourceAccess
	SUTA interfaces.SemanticUnderstandingTaskAccess
	UMA  interfaces.UserMgmtAccess
)

func SetDB(db *sql.DB) {
	DB = db
}

func SetAuthAccess(aa interfaces.AuthAccess) {
	AA = aa
}

func SetAsynqAccess(aqa interfaces.AsynqAccess) {
	AQA = aqa
}

func SetBuildTaskAccess(bta interfaces.BuildTaskAccess) {
	BTA = bta
}

func SetCatalogAccess(ca interfaces.CatalogAccess) {
	CA = ca
}

func SetConnectorTypeAccess(cta interfaces.ConnectorTypeAccess) {
	CTA = cta
}

func SetDiscoverScheduleAccess(dsa interfaces.DiscoverScheduleAccess) {
	DSA = dsa
}

func SetDiscoverTaskAccess(dta interfaces.DiscoverTaskAccess) {
	DTA = dta
}

func SetKafkaAccess(ka interfaces.KafkaAccess) {
	KA = ka
}

func SetModelFactoryAccess(mfa interfaces.ModelFactoryAccess) {
	MFA = mfa
}

func SetPermissionAccess(pa interfaces.PermissionAccess) {
	PA = pa
}

func SetResourceAccess(ra interfaces.ResourceAccess) {
	RA = ra
}

func SetSemanticUnderstandingTaskAccess(suta interfaces.SemanticUnderstandingTaskAccess) {
	SUTA = suta
}

func SetUserMgmtAccess(uma interfaces.UserMgmtAccess) {
	UMA = uma
}
