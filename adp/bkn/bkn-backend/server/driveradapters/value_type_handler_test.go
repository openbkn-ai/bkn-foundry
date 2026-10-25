// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func TestValueTypeRoutes(t *testing.T) {
	restore := setGinMode()
	defer restore()

	engine := gin.New()
	(&restHandler{appSetting: &common.AppSetting{}}).RegisterPublic(engine)
	registered := map[string]bool{}
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, prefix := range []string{
		"/api/bkn-backend/v1", "/api/bkn-backend/in/v1",
	} {
		for _, route := range []string{
			"POST /knowledge-networks/:kn_id/value-types",
			"GET /knowledge-networks/:kn_id/value-types",
			"GET /knowledge-networks/:kn_id/value-types/:vt_id",
			"PUT /knowledge-networks/:kn_id/value-types/:vt_id",
			"DELETE /knowledge-networks/:kn_id/value-types/:vt_id",
		} {
			method, path, _ := strings.Cut(route, " ")
			assert.True(t, registered[method+" "+prefix+path], "%s %s%s", method, prefix, path)
		}
	}

	for route, action := range map[string]string{
		"POST /api/bkn-backend/v1/knowledge-networks/:kn_id/value-types":          "create",
		"PUT /api/bkn-backend/v1/knowledge-networks/:kn_id/value-types/:vt_id":    "update",
		"DELETE /api/bkn-backend/v1/knowledge-networks/:kn_id/value-types/:vt_id": "delete",
	} {
		method, path, _ := strings.Cut(route, " ")
		rule, ok := registeredOperationAudit(method, path, "")
		require.True(t, ok, route)
		assert.Equal(t, action, rule.Action)
		assert.Equal(t, "value_type", rule.TargetType)
	}

	engine = gin.New()
	var target string
	engine.PUT("/knowledge-networks/:kn_id/value-types/:vt_id", func(c *gin.Context) {
		target = operationAuditPathTarget(c, "value_type")
	})
	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/knowledge-networks/kn1/value-types/amount_cny", nil))
	assert.Equal(t, "amount_cny", target)
}

func TestValueTypeHandlers(t *testing.T) {
	restore := setGinMode()
	defer restore()

	serve := func(handler *restHandler, method, path, body string) *httptest.ResponseRecorder {
		engine := gin.New()
		visitor := hydra.Visitor{ID: "user-1", Type: hydra.VisitorType_User}
		engine.POST("/knowledge-networks/:kn_id/value-types", func(c *gin.Context) { handler.CreateValueTypes(c, visitor) })
		engine.PUT("/knowledge-networks/:kn_id/value-types/:vt_id", func(c *gin.Context) { handler.UpdateValueType(c, visitor) })
		engine.DELETE("/knowledge-networks/:kn_id/value-types/:vt_id", func(c *gin.Context) { handler.DeleteValueType(c, visitor) })
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}

	t.Run("create applies the network from the path", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		kns := bmock.NewMockKNService(ctrl)
		vts := bmock.NewMockValueTypeService(ctrl)
		kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn1", "main").Return("Network 1", true, nil)
		vts.EXPECT().CreateValueTypes(gomock.Any(), nil, gomock.Any(), interfaces.ImportMode_Overwrite).
			DoAndReturn(func(_, _ any, valueTypes []*interfaces.ValueType, _ string) ([]string, error) {
				require.Len(t, valueTypes, 1)
				assert.Equal(t, "kn1", valueTypes[0].KNID)
				assert.Equal(t, "decimal", valueTypes[0].BaseType, "base_type is compared as lower case")
				assert.Equal(t, "人民币金额", valueTypes[0].VTName)
				return []string{"amount_cny"}, nil
			})
		w := serve(&restHandler{kns: kns, vts: vts}, http.MethodPost, "/knowledge-networks/kn1/value-types?import_mode=overwrite",
			`{"entries":[{"id":"amount_cny","name":" 人民币金额 ","base_type":"Decimal","range":{"min":0},"unit":"元"}]}`)
		assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		var ids []map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ids))
		assert.Equal(t, []map[string]string{{"id": "amount_cny"}}, ids)
	})

	t.Run("create refuses a name twice in the request", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		kns := bmock.NewMockKNService(ctrl)
		kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn1", "main").Return("Network 1", true, nil)
		w := serve(&restHandler{kns: kns}, http.MethodPost, "/knowledge-networks/kn1/value-types",
			`{"entries":[{"id":"a","name":"状态","base_type":"string"},{"id":"b","name":"状态","base_type":"string"}]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), berrors.BknBackend_ValueType_Duplicated_Name)
	})

	t.Run("create refuses a null enum item", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		kns := bmock.NewMockKNService(ctrl)
		kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn1", "main").Return("Network 1", true, nil)
		w := serve(&restHandler{kns: kns}, http.MethodPost, "/knowledge-networks/kn1/value-types",
			`{"entries":[{"id":"s","name":"状态","base_type":"string","enum":[null,{"value":"A","display":"甲"}]}]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "enum item 1 is null")
	})

	t.Run("update takes the ID from the path", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		kns := bmock.NewMockKNService(ctrl)
		vts := bmock.NewMockValueTypeService(ctrl)
		kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn1", "main").Return("Network 1", true, nil)
		vts.EXPECT().UpdateValueType(gomock.Any(), nil, gomock.Any()).DoAndReturn(func(_, _ any, vt *interfaces.ValueType) error {
			assert.Equal(t, "amount_cny", vt.VTID)
			return nil
		})
		w := serve(&restHandler{kns: kns, vts: vts}, http.MethodPut, "/knowledge-networks/kn1/value-types/amount_cny",
			`{"id":"ignored","name":"人民币金额","base_type":"decimal"}`)
		assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	})

	t.Run("delete reports what references the value type", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		kns := bmock.NewMockKNService(ctrl)
		vts := bmock.NewMockValueTypeService(ctrl)
		kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn1", "main").Return("Network 1", true, nil)
		vts.EXPECT().GetValueTypeByID(gomock.Any(), "kn1", "main", "amount_cny").
			Return(&interfaces.ValueType{VTID: "amount_cny", VTName: "人民币金额"}, nil)
		vts.EXPECT().DeleteValueType(gomock.Any(), nil, "kn1", "main", "amount_cny").Return(
			httperrors.NewHTTPError(context.Background(), http.StatusConflict, berrors.BknBackend_ValueType_Referenced).
				WithErrorDetails(map[string]any{"references": []interfaces.ValueTypeReference{{ObjectTypeID: "order", Property: "amount"}}}))
		w := serve(&restHandler{kns: kns, vts: vts}, http.MethodDelete, "/knowledge-networks/kn1/value-types/amount_cny", "")
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, w.Body.String(), `"object_type_id":"order"`)
	})

	t.Run("an unknown network is not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		kns := bmock.NewMockKNService(ctrl)
		kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn1", "main").Return("", false, nil)
		w := serve(&restHandler{kns: kns}, http.MethodDelete, "/knowledge-networks/kn1/value-types/amount_cny", "")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}
