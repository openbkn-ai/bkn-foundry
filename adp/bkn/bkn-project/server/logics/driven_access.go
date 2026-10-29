// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var (
	DB  *sql.DB
	AA  interfaces.AuthAccess
	RA  interfaces.ReleaseAccess
	PA  interfaces.ProjectAccess
	DA  interfaces.DraftAccess
	CA  interfaces.CheckAccess
	IRA interfaces.IDRegistryAccess
	VA  interfaces.VegaAccess
	IBA interfaces.IndexBuildAccess
	GS  interfaces.GitStore
	BBA interfaces.BknBackendAccess
	EFA interfaces.ExecutionFactoryAccess
	RQA interfaces.RequirementAccess
	BAA interfaces.BknAgentAccess
)

func SetDB(db *sql.DB) {
	DB = db
}

func SetAuthAccess(aa interfaces.AuthAccess) {
	AA = aa
}

func SetReleaseAccess(ra interfaces.ReleaseAccess) {
	RA = ra
}

func SetProjectAccess(pa interfaces.ProjectAccess) {
	PA = pa
}

func SetDraftAccess(da interfaces.DraftAccess) {
	DA = da
}

func SetCheckAccess(ca interfaces.CheckAccess) {
	CA = ca
}

func SetIDRegistryAccess(ira interfaces.IDRegistryAccess) {
	IRA = ira
}

func SetVegaAccess(va interfaces.VegaAccess) {
	VA = va
}

func SetIndexBuildAccess(iba interfaces.IndexBuildAccess) {
	IBA = iba
}

func SetGitStore(gs interfaces.GitStore) {
	GS = gs
}

func SetBknBackendAccess(bba interfaces.BknBackendAccess) {
	BBA = bba
}

func SetExecutionFactoryAccess(efa interfaces.ExecutionFactoryAccess) {
	EFA = efa
}

func SetRequirementAccess(rqa interfaces.RequirementAccess) {
	RQA = rqa
}

func SetBknAgentAccess(baa interfaces.BknAgentAccess) {
	BAA = baa
}
