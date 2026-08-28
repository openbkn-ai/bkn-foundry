package common

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// GetLanguageFromCtx Gets the language setting from context.
func GetLanguageFromCtx(ctx context.Context) Language {
	return GetLanguageByCtx(ctx)
}

// IsPublicAPIFromCtx determines whether it is a public API.
func IsPublicAPIFromCtx(ctx context.Context) bool {
	if isPublic, ok := ctx.Value(interfaces.IsPublic).(bool); ok {
		return isPublic
	}
	return false
}

func SetPublicAPIToCtx(ctx context.Context, isPublic bool) context.Context {
	return context.WithValue(ctx, interfaces.IsPublic, isPublic)
}

// SetLanguageToCtx sets the language to context.
func SetLanguageToCtx(ctx context.Context, languageInfo Language) context.Context {
	return SetLanguageByCtx(ctx, languageInfo)
}

// SetAccountAuthContextToCtx sets the account authentication context to context.
func SetAccountAuthContextToCtx(ctx context.Context, authContext *interfaces.AccountAuthContext) context.Context {
	return context.WithValue(ctx, interfaces.KeyAccountAuthContext, authContext)
}

func GetAccountAuthContextFromCtx(ctx context.Context) (*interfaces.AccountAuthContext, bool) {
	authContext, ok := ctx.Value(interfaces.KeyAccountAuthContext).(*interfaces.AccountAuthContext)
	return authContext, ok
}

// GetTokenInfoFromCtx Gets token information from context.
// func GetTokenInfoFromCtx(ctx context.Context) (*interfaces.TokenInfo, bool) {
// 	authContext, ok := GetAccountAuthContextFromCtx(ctx)
// 	if !ok {
// 		return nil, false
// 	}
// 	if authContext.TokenInfo == nil {
// 		return nil, false
// 	}
// 	return authContext.TokenInfo, true
// }

// SetExecutionModeToCtx sets execution mode to context.
func SetExecutionModeToCtx(ctx context.Context, executionMode interfaces.ExecutionMode) context.Context {
	return context.WithValue(ctx, interfaces.KeyExecutionMode, executionMode)
}

// GetExecutionModeFromCtx Gets the execution mode from context.
func GetExecutionModeFromCtx(ctx context.Context) interfaces.ExecutionMode {
	executionMode, ok := ctx.Value(interfaces.KeyExecutionMode).(interfaces.ExecutionMode)
	if !ok || executionMode == "" {
		executionMode = interfaces.ExecutionModeSync
	}
	return executionMode
}

// GetStreamingModeFromCtx Gets the streaming mode from context.
func GetStreamingModeFromCtx(ctx context.Context) (interfaces.StreamingMode, bool) {
	streamingMode, ok := ctx.Value(interfaces.KeyStreamingMode).(interfaces.StreamingMode)
	return streamingMode, ok
}

// SetStreamingModeToCtx sets streaming mode to context.
func SetStreamingModeToCtx(ctx context.Context, streamingMode interfaces.StreamingMode) context.Context {
	return context.WithValue(ctx, interfaces.KeyStreamingMode, streamingMode)
}

// SetResponseWriterToCtx sets response writer to context.
func SetResponseWriterToCtx(ctx context.Context, responseWriter http.ResponseWriter) context.Context {
	return context.WithValue(ctx, interfaces.KeyResponseWriter, responseWriter)
}

// GetResponseWriterFromCtx Gets the response writer from context.
func GetResponseWriterFromCtx(ctx context.Context) (http.ResponseWriter, bool) {
	responseWriter, ok := ctx.Value(interfaces.KeyResponseWriter).(http.ResponseWriter)
	return responseWriter, ok
}

// GetHeaderFromCtx When requesting the external interface, obtain the Header parameter from the context and pass it.
func GetHeaderFromCtx(ctx context.Context) (header map[string]string) {
	header = map[string]string{}
	header = MergeTraceHeaders(ctx, header)
	authContext, ok := GetAccountAuthContextFromCtx(ctx)
	if !ok {
		return
	}
	header[string(interfaces.HeaderXAccountID)] = authContext.AccountID
	header[string(interfaces.HeaderXAccountType)] = string(authContext.AccountType)
	return
}
