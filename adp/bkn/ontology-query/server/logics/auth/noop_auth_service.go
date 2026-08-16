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

	"ontology-query/common"
	"ontology-query/interfaces"
)

// NoopAuthService is used when authentication is disabled.
type NoopAuthService struct {
	appSetting *common.AppSetting
}

func NewNoopAuthService(appSetting *common.AppSetting) interfaces.AuthService {
	return &NoopAuthService{
		appSetting: appSetting,
	}
}

func (n *NoopAuthService) VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	// Return an empty Visitor without authentication checks.
	return hydra.Visitor{}, nil
}
