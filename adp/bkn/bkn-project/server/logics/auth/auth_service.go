// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
)

var (
	authServiceOnce sync.Once
	authService     interfaces.AuthService
)

// hydraAuthService authenticates external callers by introspecting their
// token against Hydra, as bkn-backend does.
type hydraAuthService struct {
	aa interfaces.AuthAccess
}

func NewAuthService() interfaces.AuthService {
	authServiceOnce.Do(func() {
		authService = &hydraAuthService{aa: logics.AA}
	})
	return authService
}

func (s *hydraAuthService) VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	return s.aa.VerifyToken(ctx, c)
}
