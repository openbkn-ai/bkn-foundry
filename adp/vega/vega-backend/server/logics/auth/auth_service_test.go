// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package auth

import (
	"context"
	"errors"
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
}

func TestHydraAuthServiceVerifyTokenDelegates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	expected := hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User}
	access := &fakeAuthAccess{visitor: expected}
	service := &hydraAuthService{aa: access}

	got, err := service.VerifyToken(context.Background(), c)

	require.NoError(t, err)
	assert.Equal(t, expected, got)
	assert.Same(t, c, access.gotCtx)

	access.err = errors.New("invalid token")
	_, err = service.VerifyToken(context.Background(), c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid token")
}

type fakeAuthAccess struct {
	visitor hydra.Visitor
	err     error
	gotCtx  *gin.Context
}

func (f *fakeAuthAccess) VerifyToken(_ context.Context, c *gin.Context) (hydra.Visitor, error) {
	f.gotCtx = c
	return f.visitor, f.err
}
