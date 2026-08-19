// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package errors defines application error codes.
package errors

import (
	"context"
	"fmt"
	"net/http"

	jsoniter "github.com/json-iterator/go"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/localize"
)

// HTTPError represents a public HTTP error.
type HTTPError struct {
	HTTPCode     int         `json:"-"`
	Language     string      `json:"-"`
	Code         string      `json:"code,omitempty"`
	Description  string      `json:"description,omitempty"` // Localized error description.
	Solution     string      `json:"solution,omitempty"`    // Localized remediation guidance.
	ErrorLink    string      `json:"link,omitempty"`        // Error reference link.
	ErrorDetails interface{} `json:"details,omitempty"`     // Additional public detail.
}

func (e *HTTPError) WithDescription(extCode string, params ...interface{}) *HTTPError {
	if e.HTTPCode == 0 {
		e.HTTPCode = http.StatusInternalServerError
	}
	if e.Language == "" {
		e.Language = "zh_CN"
	}
	if e.Code == "" {
		e.Code = fmt.Sprintf("Public.%d.%s", e.HTTPCode, extCode)
	}
	tr := localize.NewI18nTranslator(e.Language)
	e.Description = fmt.Sprintf(tr.Trans("desc."+extCode), params...)
	return e
}

// Error returns error message.
func (e *HTTPError) Error() string {
	errBys, _ := jsoniter.Marshal(e)
	return string(errBys)
}

var (
	errServerName = "agentRetrieval"

	errCodeMap = map[int]string{
		http.StatusBadRequest:       "BadRequest",
		http.StatusUnauthorized:     "Unauthorized",
		http.StatusForbidden:        "Forbidden",
		http.StatusNotFound:         "NotFound",
		http.StatusMethodNotAllowed: "MethodNotAllowed",
		http.StatusConflict:         "Conflict",
		// Keep 413 and 502 distinct from InternalServerError. Callers, including
		// models, use the code to decide whether a retry is appropriate.
		http.StatusRequestEntityTooLarge: "RequestEntityTooLarge",
		http.StatusBadGateway:            "BadGateway",
		http.StatusInternalServerError:   "InternalServerError",
		http.StatusNotImplemented:        "NotImplemented",
		http.StatusServiceUnavailable:    "ServiceUnavailable",
	}
)

// DefaultHTTPError creates a public error with a standard error code.
func DefaultHTTPError(ctx context.Context, httpCode int, details interface{}) *HTTPError {
	language := common.GetLanguageFromCtx(ctx)
	tr := localize.NewI18nTranslator(language)
	errCode := errCodeMap[httpCode]
	if errCode == "" {
		errCode = errCodeMap[http.StatusInternalServerError]
	}
	// Resolve solution and error link with defaults.
	solutionKey := "sol." + errCode
	solution := tr.Trans(solutionKey)
	if solution == solutionKey { // Fall back to the generic solution when missing.
		solution = tr.Trans("sol.Common")
	}

	errorLinkKey := "link." + errCode
	errorLink := tr.Trans(errorLinkKey)
	if errorLink == errorLinkKey { // Return the no-link value when missing.
		errorLink = tr.Trans("link.None")
	}

	return &HTTPError{
		HTTPCode:     httpCode,
		Language:     language,
		Code:         "Public." + errCode,
		Description:  tr.Trans("desc." + errCode),
		Solution:     solution,
		ErrorLink:    errorLink,
		ErrorDetails: details,
	}
}

// LocalizedDetail returns a localized public error detail for a stable resource key.
// The underlying error is intentionally kept in server-side logs rather than returned
// to callers.
func LocalizedDetail(ctx context.Context, key string, params ...interface{}) string {
	tr := localize.NewI18nTranslator(common.GetLanguageFromCtx(ctx))
	messageID := "detail." + key
	detail := tr.Trans(messageID)
	if detail == messageID {
		detail = tr.Trans("desc.InternalServerError")
	}
	return fmt.Sprintf(detail, params...)
}

// NewHTTPError creates an HTTPError with an extension error code.
func NewHTTPError(ctx context.Context, httpCode int, extCode string, details interface{}, descParams ...interface{}) *HTTPError {
	language := common.GetLanguageFromCtx(ctx)
	tr := localize.NewI18nTranslator(language)
	errCode := errCodeMap[httpCode]
	if errCode == "" {
		errCode = errCodeMap[http.StatusInternalServerError]
	}
	// Resolve solution and error link with defaults.
	solutionKey := "sol." + extCode
	solution := tr.Trans(solutionKey)
	if solution == solutionKey { // Fall back to the generic solution when missing.
		solution = tr.Trans("sol.Common")
	}

	errorLinkKey := "link." + extCode
	errorLink := tr.Trans(errorLinkKey)
	if errorLink == errorLinkKey { // Return the no-link value when missing.
		errorLink = tr.Trans("link.None")
	}
	return &HTTPError{
		HTTPCode:     httpCode,
		Language:     language,
		Code:         fmt.Sprintf("%s.%s.%s", errServerName, errCode, extCode),
		Description:  fmt.Sprintf(tr.Trans("desc."+extCode), descParams...), // Supports standard fmt placeholders.
		Solution:     solution,
		ErrorLink:    errorLink,
		ErrorDetails: details,
	}
}
