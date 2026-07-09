// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

// 复现 bug：嵌入字段只校验存在性不校验类型，选了非文本字段（integer/datetime 等）
// 创建成功，但运行时被当作空文本静默跳过——产出永远为空的 _vector 列且进度 100%。
// 现在创建时即拦截：embedding_field 仅允许 string/text。
func Test_BuildTaskRestHandler_CreateBuildTask_EmbeddingFieldType(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine := gin.New()
	engine.Use(gin.Recovery())

	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	rs := vmock.NewMockResourceService(mockCtrl)
	bts := vmock.NewMockBuildTaskService(mockCtrl)
	handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, rs, bts, nil, nil, nil, nil, nil, nil)
	handler.RegisterPublic(engine)

	resource := &interfaces.Resource{
		ID: "res-1",
		SchemaDefinition: []*interfaces.Property{
			{Name: "id", Type: interfaces.DataType_Integer},
			{Name: "name", Type: interfaces.DataType_String},
			{Name: "summary", Type: interfaces.DataType_Text},
			{Name: "created_at", Type: interfaces.DataType_Datetime},
		},
	}
	rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(resource, nil).AnyTimes()

	const url = "/api/vega-backend/in/v1/build-tasks"
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	t.Run("reject integer embedding field", func(t *testing.T) {
		w := post(`{"resource_id":"res-1","mode":"batch","build_key_fields":"id","embedding_fields":"id"}`)
		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "only string/text fields can be embedded")
	})

	t.Run("reject datetime embedding field mixed with valid ones", func(t *testing.T) {
		w := post(`{"resource_id":"res-1","mode":"batch","build_key_fields":"id","embedding_fields":"name,created_at"}`)
		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "created_at")
	})

	t.Run("reject unknown embedding field", func(t *testing.T) {
		w := post(`{"resource_id":"res-1","mode":"batch","build_key_fields":"id","embedding_fields":"nope"}`)
		require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Contains(t, w.Body.String(), "not found in resource schema")
	})

	t.Run("accept string and text embedding fields", func(t *testing.T) {
		bts.EXPECT().CreateBuildTask(gomock.Any(), gomock.Any()).Return("task-1", nil)
		w := post(`{"resource_id":"res-1","mode":"batch","build_key_fields":"id","embedding_fields":"name,summary"}`)
		require.Equal(t, http.StatusCreated, w.Result().StatusCode)
	})
}

// fulltext 特征支持 text 与 string（ES 风格 multi-field 下 string 同样可分词检索）；
// keyword 仍仅 string，vector 仅 vector。
func Test_IsFeatureSupported_Matrix(t *testing.T) {
	tests := []struct {
		fieldType   string
		featureType string
		want        bool
	}{
		{interfaces.DataType_Text, interfaces.PropertyFeatureType_Fulltext, true},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Fulltext, true},
		{interfaces.DataType_Integer, interfaces.PropertyFeatureType_Fulltext, false},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Keyword, true},
		{interfaces.DataType_Text, interfaces.PropertyFeatureType_Keyword, false},
		{interfaces.DataType_Vector, interfaces.PropertyFeatureType_Vector, true},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Vector, false},
		{interfaces.DataType_Text, "unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.fieldType+"/"+tt.featureType, func(t *testing.T) {
			assert.Equal(t, tt.want, IsFeatureSupported(tt.fieldType, tt.featureType))
		})
	}
}
