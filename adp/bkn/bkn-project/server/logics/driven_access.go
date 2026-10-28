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
	VA  interfaces.VegaAccess
	GS  interfaces.GitStore
	BBA interfaces.BknBackendAccess
	EFA interfaces.ExecutionFactoryAccess
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

func SetVegaAccess(va interfaces.VegaAccess) {
	VA = va
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
