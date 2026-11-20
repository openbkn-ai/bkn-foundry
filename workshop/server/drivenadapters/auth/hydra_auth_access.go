// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package auth verifies bearer credentials: OAuth tokens against Hydra, as
// bkn-backend's external routes do, and API keys against bkn-safe, as the
// knowledge network's MCP server does.
package auth

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/workshop/server/interfaces"
)

type hydraAuthAccess struct {
	hydra   hydra.Hydra
	apiKeys *apiKeyVerifier
}

// NewHydraAuthAccess returns an AuthAccess that accepts the OAuth tokens Hydra
// issued and the API keys bkn-safe issued (bak_…), at bknSafeURL.
func NewHydraAuthAccess(setting hydra.HydraAdminSetting, bknSafeURL string) interfaces.AuthAccess {
	return &hydraAuthAccess{hydra: hydra.NewHydra(setting), apiKeys: newAPIKeyVerifier(bknSafeURL)}
}

func (a *hydraAuthAccess) VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "VerifyToken")
	defer span.End()

	verify := a.hydra.VerifyToken
	if strings.HasPrefix(bearer(c), apiKeyPrefix) {
		verify = a.verifyAPIKey
	}
	visitor, err := verify(ctx, c)
	if err != nil {
		span.SetStatus(codes.Error, "Verify token failed")
		return visitor, err
	}
	span.SetStatus(codes.Ok, "")
	return visitor, nil
}

// verifyAPIKey makes the key's owner the visitor, as their OAuth token would.
func (a *hydraAuthAccess) verifyAPIKey(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	owner, err := a.apiKeys.owner(ctx, bearer(c))
	if err != nil {
		return hydra.Visitor{}, err
	}
	return hydra.Visitor{
		ID:        owner.Sub,
		TokenID:   c.GetHeader("Authorization"),
		IP:        c.ClientIP(),
		Mac:       c.GetHeader("X-Request-MAC"),
		UserAgent: c.GetHeader("User-Agent"),
		Type:      visitorType(owner.AccountType),
	}, nil
}

// bearer is the credential in the Authorization header.
func bearer(c *gin.Context) string {
	return strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
}
