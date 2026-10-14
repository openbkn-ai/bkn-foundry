// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package visitor

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func GenerateVisitor(c *gin.Context) hydra.Visitor {
	applicationPrincipalID := ""
	if strings.HasPrefix(c.Request.URL.Path, "/api/ontology-query/in/v1/") {
		applicationPrincipalID = strings.TrimSpace(c.GetHeader("X-BKN-Application-Principal-ID"))
	}
	accountInfo := interfaces.AccountInfo{
		ID:   c.GetHeader(interfaces.HTTP_HEADER_ACCOUNT_ID),
		Type: c.GetHeader(interfaces.HTTP_HEADER_ACCOUNT_TYPE),
	}

	visitor := hydra.Visitor{
		ID:         accountInfo.ID,
		Type:       hydra.VisitorType(accountInfo.Type),
		ClientID:   applicationPrincipalID,
		TokenID:    "", // No token.
		IP:         c.ClientIP(),
		Mac:        c.GetHeader("X-Request-MAC"),
		UserAgent:  c.GetHeader("User-Agent"),
		ClientType: hydra.ClientType_Linux,
	}
	return visitor
}
