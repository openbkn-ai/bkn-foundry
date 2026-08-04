package auth

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/common/visitor"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
)

type NoopAuthService struct {
	appSetting *common.AppSetting
}

func NewNoopAuthService(appSetting *common.AppSetting) interfaces.AuthService {
	return &NoopAuthService{
		appSetting: appSetting,
	}
}

func (n *NoopAuthService) VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	return visitor.GenerateVisitor(c), nil
}
