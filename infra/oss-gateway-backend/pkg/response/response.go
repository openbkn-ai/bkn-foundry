package response

import (
	stderrors "errors"
	"net/http"

	"oss-gateway/locale"
	"oss-gateway/pkg/errors"

	"github.com/gin-gonic/gin"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// Response is the OSS Gateway response envelope.
type Response struct {
	Res         int         `json:"res,omitempty"`
	Code        string      `json:"code,omitempty"`
	Message     string      `json:"message,omitempty"`
	Description string      `json:"description,omitempty"`
	Detail      string      `json:"detail,omitempty"`
	Solution    string      `json:"solution,omitempty"`
	Cause       string      `json:"cause,omitempty"`
	Data        interface{} `json:"data,omitempty"`
	Count       int         `json:"count,omitempty"`
}

type localizedError struct {
	Message     string
	Description string
	Solution    string
}

// Success writes a successful response.
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{Res: 0, Data: data})
}

// SuccessWithCount writes a successful paginated response.
func SuccessWithCount(c *gin.Context, data interface{}, count int) {
	c.JSON(http.StatusOK, Response{Count: count, Data: data})
}

// ErrorWithCode writes a localized error while preserving the stable code and
// HTTP status. Cause remains an English diagnostic in the existing response field.
func ErrorWithCode(c *gin.Context, code *errors.ErrorCode, templateData map[string]interface{}, cause string) {
	if code == nil {
		code = &errors.InternalError
	}
	text := localize(c, code, templateData)
	sharedrest.MarkLocalizedResponse(c)
	c.JSON(code.HTTPStatus, Response{
		Code:        code.Code,
		Message:     text.Message,
		Description: text.Description,
		Detail:      text.Description,
		Solution:    text.Solution,
		Cause:       cause,
	})
}

// BadRequest writes the generic invalid-request contract without exposing
// parser or validator diagnostics as public response text.
func BadRequest(c *gin.Context, diagnostic string) {
	recordDiagnostic(c, diagnostic)
	ErrorWithCode(c, &errors.BadRequest, nil, "")
}

// NotFound writes the generic missing-resource contract.
func NotFound(c *gin.Context, diagnostic string) {
	recordDiagnostic(c, diagnostic)
	ErrorWithCode(c, &errors.NotFound, nil, "")
}

// InternalError writes a localized public error without exposing internal
// implementation details to clients.
func InternalError(c *gin.Context, diagnostic string) {
	recordDiagnostic(c, diagnostic)
	ErrorWithCode(c, &errors.InternalError, nil, "")
}

// InvalidParam writes an invalid-parameter response for a stable field name.
func InvalidParam(c *gin.Context, parameter string) {
	ErrorWithCode(c, &errors.InvalidParam, map[string]interface{}{"Parameter": parameter}, "")
}

// StorageNotFound writes the missing-storage response.
func StorageNotFound(c *gin.Context) {
	ErrorWithCode(c, &errors.StorageNotFound, nil, "")
}

// ConnectionFailed writes a storage-connection failure response.
func ConnectionFailed(c *gin.Context, cause string) {
	ErrorWithCode(c, &errors.ConnectionFailed, nil, cause)
}

// StorageNameExist writes a duplicate storage-name response.
func StorageNameExist(c *gin.Context, storageName string) {
	ErrorWithCode(c, &errors.StorageNameExists, map[string]interface{}{"StorageName": storageName}, "")
}

// StorageExist writes a duplicate bucket/location response.
func StorageExist(c *gin.Context, bucket, location string) {
	ErrorWithCode(c, &errors.StorageExists, map[string]interface{}{
		"Bucket":   bucket,
		"Location": location,
	}, "")
}

// TooManyKeys writes the batch-size limit response.
func TooManyKeys(c *gin.Context, maxKeys int) {
	ErrorWithCode(c, &errors.TooManyKeys, map[string]interface{}{"MaxKeys": maxKeys}, "")
}

// InvalidVendorType writes an unsupported-vendor response.
func InvalidVendorType(c *gin.Context, vendorType string) {
	ErrorWithCode(c, &errors.InvalidVendorType, map[string]interface{}{"VendorType": vendorType}, "")
}

// DefaultStorageExists writes a duplicate-default-storage response.
func DefaultStorageExists(c *gin.Context, existingStorageName string) {
	ErrorWithCode(c, &errors.DefaultStorageExists, map[string]interface{}{"StorageName": existingStorageName}, "")
}

// ServiceNotReady preserves the readiness response shape while localizing its
// human-readable fields.
func ServiceNotReady(c *gin.Context, checks map[string]string) {
	code := &errors.ServiceNotReady
	text := localize(c, code, nil)
	sharedrest.MarkLocalizedResponse(c)
	c.JSON(code.HTTPStatus, gin.H{
		"code":        code.Code,
		"message":     text.Message,
		"description": text.Description,
		"solution":    text.Solution,
		"checks":      checks,
	})
}

func localize(c *gin.Context, code *errors.ErrorCode, templateData map[string]interface{}) localizedError {
	ctx := c.Request.Context()
	return localizedError{
		Message:     locale.Translate(ctx, code.MessageID+".Message", templateData),
		Description: locale.Translate(ctx, code.MessageID+".Description", templateData),
		Solution:    locale.Translate(ctx, code.MessageID+".Solution", templateData),
	}
}

func recordDiagnostic(c *gin.Context, diagnostic string) {
	if diagnostic == "" {
		return
	}
	_ = c.Error(stderrors.New(diagnostic)).SetType(gin.ErrorTypePrivate)
}
