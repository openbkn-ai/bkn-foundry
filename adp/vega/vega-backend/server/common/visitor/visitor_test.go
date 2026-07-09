// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package visitor

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/stretchr/testify/assert"

	"vega-backend/interfaces"
)

func TestGenerateVisitor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("builds visitor from request headers", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest("GET", "/catalogs", nil)
		req.RemoteAddr = "192.0.2.10:12345"
		req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "account-1")
		req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, string(hydra.VisitorType_User))
		req.Header.Set("X-Request-MAC", "00:11:22:33:44:55")
		req.Header.Set("User-Agent", "vega-test")
		c.Request = req

		got := GenerateVisitor(c)

		assert.Equal(t, "account-1", got.ID)
		assert.Equal(t, hydra.VisitorType_User, got.Type)
		assert.Empty(t, got.TokenID)
		assert.Equal(t, "192.0.2.10", got.IP)
		assert.Equal(t, "00:11:22:33:44:55", got.Mac)
		assert.Equal(t, "vega-test", got.UserAgent)
		assert.Equal(t, hydra.ClientType_Linux, got.ClientType)
	})

	t.Run("keeps empty account headers empty", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest("GET", "/catalogs", nil)
		req.RemoteAddr = "192.0.2.11:12345"
		c.Request = req

		got := GenerateVisitor(c)

		assert.Empty(t, got.ID)
		assert.Empty(t, got.Type)
		assert.Empty(t, got.TokenID)
		assert.Equal(t, "192.0.2.11", got.IP)
		assert.Equal(t, hydra.ClientType_Linux, got.ClientType)
	})
}
