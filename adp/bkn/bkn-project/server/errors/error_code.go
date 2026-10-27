// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package errors defines service error codes.
package errors

import (
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/locale"
)

// Public error codes available to all service modules.
const (
	// 400
	BknProject_InvalidParameter_Limit  = "BknProject.InvalidParameter.Limit"
	BknProject_InvalidParameter_Cursor = "BknProject.InvalidParameter.Cursor"
)

var (
	errCodeList = []string{
		// 400
		BknProject_InvalidParameter_Limit,
		BknProject_InvalidParameter_Cursor,
	}
)

// init prepares messages before registering the service error codes.
func init() {
	locale.Register()

	httperrors.Register(errCodeList)
	httperrors.Register(ReleaseErrCodeList)
	httperrors.Register(LineageErrCodeList)
	httperrors.Register(ProjectErrCodeList)
}
