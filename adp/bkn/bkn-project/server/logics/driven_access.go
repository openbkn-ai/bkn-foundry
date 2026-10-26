// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var (
	DB *sql.DB
	AA interfaces.AuthAccess
	RA interfaces.ReleaseAccess
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
