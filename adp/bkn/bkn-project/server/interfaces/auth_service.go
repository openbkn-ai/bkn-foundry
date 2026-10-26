// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
)

//go:generate mockgen -source ../interfaces/auth_service.go -destination ../interfaces/mock/mock_auth_service.go
type AuthService interface {
	VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error)
}
