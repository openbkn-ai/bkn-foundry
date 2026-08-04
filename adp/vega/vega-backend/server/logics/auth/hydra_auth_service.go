package auth

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics"
)

type hydraAuthService struct {
	appSetting *common.AppSetting
	aa         interfaces.AuthAccess
}

func NewHydraAuthService(appSetting *common.AppSetting) interfaces.AuthService {
	return &hydraAuthService{
		appSetting: appSetting,
		aa:         logics.AA,
	}
}

func (s *hydraAuthService) VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	return s.aa.VerifyToken(ctx, c)
}
