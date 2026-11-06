// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package interfaces holds the ports the community skeleton shares with the
// build that assembles Workshop's business routes.
package interfaces

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
)

// AuthAccess verifies the caller's bearer token.
type AuthAccess interface {
	VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error)
}
