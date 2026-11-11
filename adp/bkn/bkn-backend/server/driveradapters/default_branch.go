// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// defaultBranchName is what callers may name the main branch: the content a network has before
// it is versioned, and the running content after. Storage, and responses, keep calling it main.
const defaultBranchName = "default"

// defaultBranchAlias reads branch=default as branch=main, for every route.
func defaultBranchAlias() gin.HandlerFunc {
	return func(c *gin.Context) {
		query := c.Request.URL.Query()
		if query.Get("branch") == defaultBranchName {
			query.Set("branch", interfaces.MAIN_BRANCH)
			c.Request.URL.RawQuery = query.Encode()
		}
		c.Next()
	}
}
