package errors

import "net/http"

// ErrorCode defines the stable machine-readable error contract. Human-readable
// fields are resolved from MessageID using the effective request locale.
type ErrorCode struct {
	Code       string
	MessageID  string
	HTTPStatus int
}

var (
	BadRequest = ErrorCode{
		Code:       "400000",
		MessageID:  "OssGateway.BadRequest",
		HTTPStatus: http.StatusBadRequest,
	}
	InvalidParam = ErrorCode{
		Code:       "400001",
		MessageID:  "OssGateway.InvalidParameter",
		HTTPStatus: http.StatusBadRequest,
	}
	TooManyKeys = ErrorCode{
		Code:       "400002",
		MessageID:  "OssGateway.TooManyKeys",
		HTTPStatus: http.StatusBadRequest,
	}
	NotFound = ErrorCode{
		Code:       "404000",
		MessageID:  "OssGateway.NotFound",
		HTTPStatus: http.StatusNotFound,
	}
	StorageNotFound = ErrorCode{
		Code:       "404001",
		MessageID:  "OssGateway.StorageNotFound",
		HTTPStatus: http.StatusNotFound,
	}
	InternalError = ErrorCode{
		Code:       "500000",
		MessageID:  "OssGateway.InternalError",
		HTTPStatus: http.StatusInternalServerError,
	}
	ConnectionFailed = ErrorCode{
		Code:       "500001",
		MessageID:  "OssGateway.ConnectionFailed",
		HTTPStatus: http.StatusInternalServerError,
	}
	ServiceNotReady = ErrorCode{
		Code:       "503000",
		MessageID:  "OssGateway.ServiceNotReady",
		HTTPStatus: http.StatusServiceUnavailable,
	}
	StorageNameExists = ErrorCode{
		Code:       "400031107",
		MessageID:  "OssGateway.StorageNameExists",
		HTTPStatus: http.StatusBadRequest,
	}
	StorageExists = ErrorCode{
		Code:       "400031108",
		MessageID:  "OssGateway.StorageExists",
		HTTPStatus: http.StatusBadRequest,
	}
	StorageDisabled = ErrorCode{
		Code:       "400031109",
		MessageID:  "OssGateway.StorageDisabled",
		HTTPStatus: http.StatusBadRequest,
	}
	InvalidVendorType = ErrorCode{
		Code:       "400031110",
		MessageID:  "OssGateway.InvalidVendorType",
		HTTPStatus: http.StatusBadRequest,
	}
	InvalidEndpoint = ErrorCode{
		Code:       "400031111",
		MessageID:  "OssGateway.InvalidEndpoint",
		HTTPStatus: http.StatusBadRequest,
	}
	DefaultStorageExists = ErrorCode{
		Code:       "400031112",
		MessageID:  "OssGateway.DefaultStorageExists",
		HTTPStatus: http.StatusBadRequest,
	}
	Unauthorized = ErrorCode{
		Code:       "401000",
		MessageID:  "OssGateway.Unauthorized",
		HTTPStatus: http.StatusUnauthorized,
	}
	Forbidden = ErrorCode{
		Code:       "403000",
		MessageID:  "OssGateway.Forbidden",
		HTTPStatus: http.StatusForbidden,
	}
)

// GetErrorByCode returns the error definition for a stable numeric code.
func GetErrorByCode(code string) *ErrorCode {
	errorMap := map[string]*ErrorCode{
		"400000":    &BadRequest,
		"400001":    &InvalidParam,
		"400002":    &TooManyKeys,
		"404000":    &NotFound,
		"404001":    &StorageNotFound,
		"500000":    &InternalError,
		"500001":    &ConnectionFailed,
		"503000":    &ServiceNotReady,
		"400031107": &StorageNameExists,
		"400031108": &StorageExists,
		"400031109": &StorageDisabled,
		"400031110": &InvalidVendorType,
		"400031111": &InvalidEndpoint,
		"400031112": &DefaultStorageExists,
		"401000":    &Unauthorized,
		"403000":    &Forbidden,
	}
	return errorMap[code]
}
