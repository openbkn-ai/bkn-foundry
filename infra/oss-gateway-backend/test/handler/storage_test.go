package handler_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"oss-gateway/internal/handler"
	"oss-gateway/internal/service"
	"oss-gateway/pkg/adapter"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockStorageService is a mock implementation of StorageService.
type MockStorageService struct {
	mock.Mock
}

func (m *MockStorageService) Create(ctx context.Context, req *service.CreateStorageRequest) (string, error) {
	args := m.Called(ctx, req)
	return args.String(0), args.Error(1)
}

func (m *MockStorageService) Update(ctx context.Context, storageID string, req *service.UpdateStorageRequest) error {
	args := m.Called(ctx, storageID, req)
	return args.Error(0)
}

func (m *MockStorageService) Delete(ctx context.Context, storageID string) error {
	args := m.Called(ctx, storageID)
	return args.Error(0)
}

func (m *MockStorageService) Get(ctx context.Context, storageID string) (*service.StorageResponse, error) {
	args := m.Called(ctx, storageID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*service.StorageResponse), args.Error(1)
}

func (m *MockStorageService) List(ctx context.Context, req *service.ListStorageRequest) (*service.ListStorageResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*service.ListStorageResponse), args.Error(1)
}

func (m *MockStorageService) CheckConnection(ctx context.Context, storageID string) error {
	args := m.Called(ctx, storageID)
	return args.Error(0)
}

func (m *MockStorageService) GetAdapter(ctx context.Context, storageID string, useInternal bool) (adapter.OSSAdapter, error) {
	args := m.Called(ctx, storageID, useInternal)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(adapter.OSSAdapter), args.Error(1)
}

func setupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func TestStorageHandler_Create_Success(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.POST("/storages", h.Create)

	req := service.CreateStorageRequest{
		StorageName:     "test-storage",
		VendorType:      "OSS",
		Endpoint:        "https://oss-cn-hangzhou.aliyuncs.com",
		BucketName:      "test-bucket",
		AccessKeyID:     "test-key-id",
		AccessKeySecret: "test-secret",
		Region:          "cn-hangzhou",
	}

	mockService.On("Create", mock.Anything, mock.IsType((*service.CreateStorageRequest)(nil))).
		Return("storage123", nil)

	body, _ := jsoncodec.Marshal(req)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/storages", bytes.NewBuffer(body))
	r.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	mockService.AssertExpectations(t)
}

func TestStorageHandler_Create_InvalidJSON(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.POST("/storages", h.Create)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/storages", bytes.NewBufferString("{invalid json}"))
	r.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStorageHandler_Update_InvalidJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "duplicate name", body: `{"storage_name":"original","storage_name":"replacement"}`},
		{name: "duplicate name with different case", body: `{"is_enabled":true,"IS_ENABLED":false}`},
		{name: "second JSON value", body: `{} {}`},
		{name: "invalid UTF-8", body: "{\"storage_name\":\"\xff\"}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockStorageService)
			mockService.On("Update", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			h := handler.NewStorageHandler(mockService)
			router := setupRouter()
			router.PUT("/storages/:id", h.Update)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/storages/storage123", bytes.NewBufferString(tt.body))
			r.Header.Set("Content-Type", "application/json")

			router.ServeHTTP(w, r)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), `"code":"400000"`)
			mockService.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestStorageHandler_Create_ValidationError_StorageNameExists(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.POST("/storages", h.Create)

	req := service.CreateStorageRequest{
		StorageName:     "existing-storage",
		VendorType:      "OSS",
		Endpoint:        "https://oss-cn-hangzhou.aliyuncs.com",
		BucketName:      "test-bucket",
		AccessKeyID:     "test-key-id",
		AccessKeySecret: "test-secret",
		Region:          "cn-hangzhou",
	}

	mockService.On("Create", mock.Anything, mock.IsType((*service.CreateStorageRequest)(nil))).
		Return("", &service.StorageValidationError{
			Code:   "400031107",
			Params: map[string]interface{}{"StorageName": "existing-storage"},
		})

	body, _ := jsoncodec.Marshal(req)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/storages", bytes.NewBuffer(body))
	r.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"400031107"`)
	assert.Contains(t, w.Body.String(), "existing-storage")
	mockService.AssertExpectations(t)
}

func TestStorageHandler_Create_ValidationError_StorageExists(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.POST("/storages", h.Create)

	req := service.CreateStorageRequest{
		StorageName:     "test-storage",
		VendorType:      "OSS",
		Endpoint:        "https://oss-cn-hangzhou.aliyuncs.com",
		BucketName:      "existing-bucket",
		AccessKeyID:     "test-key-id",
		AccessKeySecret: "test-secret",
		Region:          "cn-hangzhou",
	}

	mockService.On("Create", mock.Anything, mock.IsType((*service.CreateStorageRequest)(nil))).
		Return("", &service.StorageValidationError{
			Code: "400031108",
			Params: map[string]interface{}{
				"Bucket":   "existing-bucket",
				"Location": "https://oss-cn-hangzhou.aliyuncs.com",
			},
		})

	body, _ := jsoncodec.Marshal(req)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/storages", bytes.NewBuffer(body))
	r.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"400031108"`)
	assert.Contains(t, w.Body.String(), "existing-bucket")
	assert.Contains(t, w.Body.String(), "https://oss-cn-hangzhou.aliyuncs.com")
	mockService.AssertExpectations(t)
}

func TestStorageHandler_Create_ValidationError_InvalidVendorType(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.POST("/storages", h.Create)

	req := service.CreateStorageRequest{
		StorageName:     "test-storage",
		VendorType:      "INVALID",
		Endpoint:        "https://oss-cn-hangzhou.aliyuncs.com",
		BucketName:      "test-bucket",
		AccessKeyID:     "test-key-id",
		AccessKeySecret: "test-secret",
		Region:          "cn-hangzhou",
	}

	mockService.On("Create", mock.Anything, mock.IsType((*service.CreateStorageRequest)(nil))).
		Return("", &service.StorageValidationError{
			Code:   "400031110",
			Params: map[string]interface{}{"VendorType": "INVALID"},
		})

	body, _ := jsoncodec.Marshal(req)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/storages", bytes.NewBuffer(body))
	r.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"400031110"`)
	assert.Contains(t, w.Body.String(), "INVALID")
	mockService.AssertExpectations(t)
}

func TestStorageHandler_Update_Success(t *testing.T) {
	enabled := false
	tests := []struct {
		name      string
		body      string
		isEnabled *bool
	}{
		{name: "exact names", body: `{"storage_name":"updated-storage","is_enabled":false,"extra":true}`, isEnabled: &enabled},
		{name: "mixed case names", body: `{"Storage_Name":"updated-storage","IS_ENABLED":false}`, isEnabled: &enabled},
		{name: "different delimiter", body: `{"storage_name":"updated-storage","IsEnabled":false,"is-enabled":false}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockStorageService)
			h := handler.NewStorageHandler(mockService)
			router := setupRouter()
			router.PUT("/storages/:id", h.Update)

			var actual *service.UpdateStorageRequest
			mockService.On("Update", mock.Anything, "storage123", mock.IsType((*service.UpdateStorageRequest)(nil))).
				Run(func(args mock.Arguments) { actual = args.Get(2).(*service.UpdateStorageRequest) }).
				Return(nil).Once()

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/storages/storage123", bytes.NewBufferString(tt.body))
			r.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, r)

			assert.Equal(t, http.StatusOK, w.Code)
			mockService.AssertExpectations(t)
			assert.Equal(t, &service.UpdateStorageRequest{
				StorageName: "updated-storage",
				IsEnabled:   tt.isEnabled,
			}, actual)
		})
	}
}

func TestStorageHandler_Update_Error(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.PUT("/storages/:id", h.Update)

	req := service.UpdateStorageRequest{
		StorageName: "updated-storage",
	}

	mockService.On("Update", mock.Anything, "storage123", mock.IsType((*service.UpdateStorageRequest)(nil))).
		Return(errors.New("update failed"))

	body, _ := jsoncodec.Marshal(req)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/storages/storage123", bytes.NewBuffer(body))
	r.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockService.AssertExpectations(t)
}

func TestStorageHandler_Delete_Success(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.DELETE("/storages/:id", h.Delete)

	mockService.On("Delete", mock.Anything, "storage123").Return(nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/storages/storage123", nil)

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	mockService.AssertExpectations(t)
}

func TestStorageHandler_Get_Success(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.GET("/storages/:id", h.Get)

	expectedResponse := &service.StorageResponse{
		StorageID:   "storage123",
		StorageName: "test-storage",
		VendorType:  "OSS",
		Endpoint:    "https://oss-cn-hangzhou.aliyuncs.com",
		BucketName:  "test-bucket",
		Region:      "cn-hangzhou",
		IsDefault:   true,
		IsEnabled:   true,
	}

	mockService.On("Get", mock.Anything, "storage123").Return(expectedResponse, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/storages/storage123", nil)

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	mockService.AssertExpectations(t)
}

func TestStorageHandler_Get_NotFound(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.GET("/storages/:id", h.Get)

	mockService.On("Get", mock.Anything, "nonexistent").Return(nil, errors.New("not found"))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/storages/nonexistent", nil)

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"404001"`)
	mockService.AssertExpectations(t)
}

func TestStorageHandler_List_Success(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.GET("/storages", h.List)

	expectedResponse := &service.ListStorageResponse{
		Count: 2,
		Data: []*service.StorageResponse{
			{
				StorageID:   "storage1",
				StorageName: "storage-1",
				VendorType:  "OSS",
			},
			{
				StorageID:   "storage2",
				StorageName: "storage-2",
				VendorType:  "OBS",
			},
		},
	}

	mockService.On("List", mock.Anything, mock.IsType((*service.ListStorageRequest)(nil))).
		Return(expectedResponse, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/storages?page=1&size=10", nil)

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	mockService.AssertExpectations(t)
}

func TestStorageHandler_CheckConnection_Success(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.POST("/storages/:id/check", h.CheckConnection)

	mockService.On("CheckConnection", mock.Anything, "storage123").Return(nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/storages/storage123/check", nil)

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	mockService.AssertExpectations(t)
}

func TestStorageHandler_CheckConnection_Failed(t *testing.T) {
	mockService := new(MockStorageService)
	h := handler.NewStorageHandler(mockService)
	router := setupRouter()
	router.POST("/storages/:id/check", h.CheckConnection)

	mockService.On("CheckConnection", mock.Anything, "storage123").
		Return(errors.New("connection failed"))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/storages/storage123/check", nil)

	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockService.AssertExpectations(t)
}
