// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var (
	haAccessOnce sync.Once
	haAccess     interfaces.AuthAccess
)

type hydraAuthAccess struct {
	appSetting *common.AppSetting
	hydra      hydra.Hydra
}

func NewHydraAuthAccess(appSetting *common.AppSetting) interfaces.AuthAccess {
	haAccessOnce.Do(func() {
		haAccess = &hydraAuthAccess{
			appSetting: appSetting,
			hydra:      hydra.NewHydra(appSetting.HydraAdminSetting),
		}
	})

	return haAccess
}

func (haa *hydraAuthAccess) VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "VerifyToken")
	defer span.End()

	visitor, err := haa.hydra.VerifyToken(ctx, c)
	if err != nil {
		span.SetStatus(codes.Error, "Verify token failed")
		return visitor, err
	}

	span.SetStatus(codes.Ok, "")
	return visitor, nil
}
