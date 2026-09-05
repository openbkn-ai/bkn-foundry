// Package interfaces define interfaces.
// @file infra.go
// @description: Define infrastructure interface.
package interfaces

import (
	"context"
	"net/url"
)

// ContextKey Context Value Key
//
//go:generate mockgen -source=infra.go -destination=../mocks/infra.go -package=mocks
type ContextKey string

const (
	// token key in KeyToken context.
	KeyToken ContextKey = "token"
	// ip key in KeyIP context.
	KeyIP ContextKey = "ip"
	// OperationID API operation unique identifier.
	OperationID ContextKey = "operationID"
	// FileNameKey file name.
	FileNameKey ContextKey = "FileName"
	// Headers header request parameters.
	Headers ContextKey = "headers"
	// UserAgent user agent
	UserAgent ContextKey = "User-Agent"
	// IsPublic Is it public?.
	IsPublic ContextKey = "is_public"
	// KeyRequestID request ID.
	KeyRequestID ContextKey = "request_id"
	// context key.
	KeyResponseWriter ContextKey = "response_writer" // response writer.
	KeyExecutionMode  ContextKey = "execution_mode"  // execution mode.
	KeyStreamingMode  ContextKey = "streaming_mode"  // streaming mode.
	// KeyAccountAuthContext account authentication context.
	KeyAccountAuthContext ContextKey = "account_auth_context"
)

// HeaderKey context key.
type HeaderKey string

const (
	// HeaderXAccountID Account ID header parameter.
	HeaderXAccountID HeaderKey = "x-account-id"
	// HeaderXAccountType account type header parameter.
	HeaderXAccountType HeaderKey = "x-account-type"
	// HeaderUserID User ID.
	HeaderUserID HeaderKey = "user_id"
	// HeaderBKNConversationID managed Conversation the call belongs to.
	HeaderBKNConversationID HeaderKey = "bkn-conversation-id"
	// HeaderBKNInteractionID managed Interaction the call belongs to.
	HeaderBKNInteractionID HeaderKey = "bkn-interaction-id"
	// HeaderBKNParentOperationID is the persisted operation invoking the Function.
	HeaderBKNParentOperationID HeaderKey = "bkn-parent-operation-id"
)

const ()

const (
	HTTP  = "http"  // http protocol.
	HTTPS = "https" // https protocol.
)

// Logger log interface.
type Logger interface {
	Debug(v ...interface{})
	Info(v ...interface{})
	Warn(v ...interface{})
	Error(v ...interface{})
	Debugf(format string, v ...interface{})
	Infof(format string, v ...interface{})
	Warnf(format string, v ...interface{})
	Errorf(format string, v ...interface{})
	WithContext(ctx context.Context) Logger
}

// HTTPClient HTTP client service interface.
type HTTPClient interface {
	Get(ctx context.Context, url string, queryValues url.Values, headers map[string]string) (respCode int, respData interface{}, err error)
	GetNoUnmarshal(ctx context.Context, url string, queryValues url.Values, headers map[string]string) (respCode int, respBody []byte, err error)
	Delete(ctx context.Context, url string, headers map[string]string) (respCode int, respData interface{}, err error)
	DeleteNoUnmarshal(ctx context.Context, url string, headers map[string]string) (respCode int, respBody []byte, err error)
	Post(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respData interface{}, err error)
	PostNoUnmarshal(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respBody []byte, err error)
	Put(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respData interface{}, err error)
	PutNoUnmarshal(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respBody []byte, err error)
	Patch(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respData interface{}, err error)
	PatchNoUnmarshal(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respBody []byte, err error)
	PostStream(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (chan string, chan error, error)
}

// Cache cache interface.
type Cache interface {
	Set(key string, value interface{})
	Get(key string) (interface{}, bool)
	Delete(key string)
	Size() int
}

// MetricLogger Metric Logger.
type MetricLogger interface {
	Log(ctx context.Context, logType string, params interface{}) (err error)
}

// Validator verification interface: used to verify operator name, description, number of single imports, imported data size, etc.
type Validator interface {
	ValidateOperatorName(ctx context.Context, name string) (err error)
	ValidateOperatorDesc(ctx context.Context, desc string) (err error)
	ValidateOperatorImportCount(ctx context.Context, count int64) (err error)
	ValidateOperatorImportSize(ctx context.Context, size int64) (err error)
	ValidatorToolBoxName(ctx context.Context, name string) (err error)
	ValidatorToolBoxDesc(ctx context.Context, desc string) (err error)
	ValidatorToolName(ctx context.Context, name string) (err error)
	ValidatorToolDesc(ctx context.Context, desc string) (err error)
	ValidatorMCPName(ctx context.Context, name string) (err error)
	ValidatorMCPDesc(ctx context.Context, desc string) (err error)
	ValidatorCategoryName(ctx context.Context, name string) (err error)
	ValidatorStruct(ctx context.Context, obj interface{}) (err error)
	ValidatorURL(ctx context.Context, url string) (err error)
	VisitorParameterDef(ctx context.Context, paramDef *ParameterDef) (err error)
}
