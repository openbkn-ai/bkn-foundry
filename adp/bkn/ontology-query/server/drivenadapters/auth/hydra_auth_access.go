// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package auth

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

type hydraAuthAccess struct {
	hydra hydra.Hydra
}

func NewHydraAuthAccess(appSetting *common.AppSetting) interfaces.AuthAccess {
	return &hydraAuthAccess{
		hydra: hydra.NewHydra(appSetting.HydraAdminSetting),
	}
}

func (h *hydraAuthAccess) VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	return h.hydra.VerifyToken(ctx, c)
}
