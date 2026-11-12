// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// AccountNameAccess looks up the display names of accounts, so a project member who may not list
// users still sees who owns a project or published a version.
//
// Names never fails: an account it cannot name is left out, and the caller shows what it has.
type AccountNameAccess interface {
	Names(ctx context.Context, accounts []AccountInfo) map[AccountInfo]string
}
