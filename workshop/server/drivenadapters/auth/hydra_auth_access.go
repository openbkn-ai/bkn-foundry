// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package auth verifies bearer tokens against Hydra, as bkn-backend's external
// routes do.
package auth

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/workshop/server/interfaces"
)

type hydraAuthAccess struct {
	hydra hydra.Hydra
}

// NewHydraAuthAccess returns an AuthAccess backed by Hydra's admin introspection.
func NewHydraAuthAccess(setting hydra.HydraAdminSetting) interfaces.AuthAccess {
	return &hydraAuthAccess{hydra: hydra.NewHydra(setting)}
}

func (a *hydraAuthAccess) VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "VerifyToken")
	defer span.End()

	visitor, err := a.hydra.VerifyToken(ctx, c)
	if err != nil {
		span.SetStatus(codes.Error, "Verify token failed")
		return visitor, err
	}
	span.SetStatus(codes.Ok, "")
	return visitor, nil
}
