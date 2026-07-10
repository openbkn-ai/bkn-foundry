package auth

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func TestNoopAuthServiceVerifyToken(t *testing.T) {
	t.Run("generates visitor from request headers", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "u1")
		req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, interfaces.ACCESSOR_TYPE_USER)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = req
		service := NewNoopAuthService(&common.AppSetting{})

		got, err := service.VerifyToken(context.Background(), c)

		require.NoError(t, err)
		assert.Equal(t, "u1", got.ID)
		assert.Equal(t, hydra.VisitorType_User, got.Type)
	})
}
