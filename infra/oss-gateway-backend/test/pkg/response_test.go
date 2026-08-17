package pkg_test

import (
	"net/http"
	"net/http/httptest"
	"oss-gateway/internal/middleware"
	"oss-gateway/pkg/errors"
	"oss-gateway/pkg/response"
	"testing"

	"github.com/gin-gonic/gin"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
)

func setupTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request = req.WithContext(sharedrest.WithLanguage(req.Context(), sharedrest.SimplifiedChinese))
	return c, w
}

func TestSuccess(t *testing.T) {
	c, w := setupTestContext()

	data := map[string]string{
		"id":   "123",
		"name": "test",
	}

	response.Success(c, data)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "\"id\":\"123\"")
}

func TestSuccessWithCount(t *testing.T) {
	c, w := setupTestContext()

	data := []map[string]string{
		{"id": "1", "name": "item1"},
		{"id": "2", "name": "item2"},
	}

	response.SuccessWithCount(c, data, 2)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "\"count\":2")
}

func TestError(t *testing.T) {
	c, w := setupTestContext()

	response.ErrorWithCode(c, &errors.InvalidParam, map[string]interface{}{"Parameter": "field"}, "missing field")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "\"code\":\"400001\"")
	assert.Contains(t, w.Body.String(), "\"message\":\"参数无效\"")
}

func TestBadRequest(t *testing.T) {
	c, w := setupTestContext()

	response.BadRequest(c, "Invalid JSON")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "请求参数无效")
	assert.NotContains(t, w.Body.String(), "Invalid JSON")
}

func TestNotFound(t *testing.T) {
	c, w := setupTestContext()

	response.NotFound(c, "Resource not found")

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "资源不存在")
	assert.NotContains(t, w.Body.String(), "Resource not found")
}

func TestInternalError(t *testing.T) {
	c, w := setupTestContext()

	response.InternalError(c, "Database connection failed")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "服务内部错误")
	assert.NotContains(t, w.Body.String(), "Database connection failed")
}

func TestInvalidParam(t *testing.T) {
	c, w := setupTestContext()

	response.InvalidParam(c, "storage_id")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "storage_id")
}

func TestStorageNotFound(t *testing.T) {
	c, w := setupTestContext()

	response.StorageNotFound(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), errors.StorageNotFound.Code)
}

func TestConnectionFailed(t *testing.T) {
	c, w := setupTestContext()

	response.ConnectionFailed(c, "timeout")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "timeout")
}

func TestStorageNameExist(t *testing.T) {
	c, w := setupTestContext()

	response.StorageNameExist(c, "my-storage")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "my-storage")
	assert.Contains(t, w.Body.String(), errors.StorageNameExists.Code)
}

func TestStorageExist(t *testing.T) {
	c, w := setupTestContext()

	response.StorageExist(c, "test-bucket", "https://oss.aliyuncs.com")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "test-bucket")
	assert.Contains(t, w.Body.String(), errors.StorageExists.Code)
}

func TestTooManyKeys(t *testing.T) {
	c, w := setupTestContext()

	response.TooManyKeys(c, 100)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "100")
	assert.Contains(t, w.Body.String(), errors.TooManyKeys.Code)
}

func TestInvalidVendorType(t *testing.T) {
	c, w := setupTestContext()

	response.InvalidVendorType(c, "INVALID_TYPE")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_TYPE")
	assert.Contains(t, w.Body.String(), errors.InvalidVendorType.Code)
}

func TestErrorWithCode(t *testing.T) {
	c, w := setupTestContext()

	response.ErrorWithCode(c, &errors.BadRequest, nil, "Custom message")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Custom message")
	assert.Contains(t, w.Body.String(), errors.BadRequest.Code)
}

func TestErrorWithCode_DefaultMessage(t *testing.T) {
	c, w := setupTestContext()

	response.ErrorWithCode(c, &errors.BadRequest, nil, "")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "请求参数无效")
}

func TestSuccess_WithNilData(t *testing.T) {
	c, w := setupTestContext()

	response.Success(c, nil)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSuccess_WithEmptyData(t *testing.T) {
	c, w := setupTestContext()

	response.Success(c, map[string]string{})

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "data")
}

func TestSuccessWithCount_ZeroCount(t *testing.T) {
	c, w := setupTestContext()

	response.SuccessWithCount(c, []interface{}{}, 0)

	assert.Equal(t, http.StatusOK, w.Code)
	// count=0 may be omitted from the JSON response.
}

func TestSuccessWithCount_LargeCount(t *testing.T) {
	c, w := setupTestContext()

	response.SuccessWithCount(c, []interface{}{}, 1000000)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "\"count\":1000000")
}

func TestInvalidParamNegotiatesLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		header         string
		language       string
		wantLanguage   string
		wantMessage    string
		wantStableCode string
	}{
		{
			name:           "accept language selects Chinese",
			header:         sharedrest.AcceptLanguageHeader,
			language:       "zh-CN",
			wantLanguage:   "zh-CN",
			wantMessage:    "参数无效",
			wantStableCode: errors.InvalidParam.Code,
		},
		{
			name:           "accept language selects English",
			header:         sharedrest.AcceptLanguageHeader,
			language:       "en-US",
			wantLanguage:   "en-US",
			wantMessage:    "Invalid parameter",
			wantStableCode: errors.InvalidParam.Code,
		},
		{
			name:           "legacy header remains compatible",
			header:         "X-Language",
			language:       "en_US",
			wantLanguage:   "en-US",
			wantMessage:    "Invalid parameter",
			wantStableCode: errors.InvalidParam.Code,
		},
		{
			name:           "unsupported language falls back to Chinese",
			header:         sharedrest.AcceptLanguageHeader,
			language:       "fr-FR",
			wantLanguage:   "zh-CN",
			wantMessage:    "参数无效",
			wantStableCode: errors.InvalidParam.Code,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Use(middleware.Language())
			router.GET("/error", func(c *gin.Context) {
				response.InvalidParam(c, "object_key")
			})

			request := httptest.NewRequest(http.MethodGet, "/error", nil)
			request.Header.Set(tt.header, tt.language)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.Equal(t, tt.wantLanguage, recorder.Header().Get(sharedrest.ContentLanguageHeader))
			assert.Contains(t, recorder.Body.String(), tt.wantMessage)
			assert.Contains(t, recorder.Body.String(), tt.wantStableCode)
		})
	}
}
