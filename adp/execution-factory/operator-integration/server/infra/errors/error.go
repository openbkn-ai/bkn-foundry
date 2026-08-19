// Package errors define error codes.
// @file errors.go
// @description: Unified handling of error codes.
package errors

import (
	"context"
	"fmt"
	"net/http"

	jsoniter "github.com/json-iterator/go"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/localize"
)

// HTTPError HTTP error.
type HTTPError struct {
	HTTPCode     int         `json:"-"`
	Language     string      `json:"-"`
	Code         string      `json:"code,omitempty"`
	Description  string      `json:"description,omitempty"` // Error description.
	Solution     string      `json:"solution,omitempty"`    // Solution.
	ErrorLink    string      `json:"link,omitempty"`        // Bad link.
	ErrorDetails interface{} `json:"details,omitempty"`     // Details.
	// DescriptionTemplateData map[string]any `json:"-"` // Error description parameter.
	// SolutionTemplateData map[string]any `json:"-"` // Solution parameters.
}

func (e *HTTPError) WithDescription(extCode ErrorCode, params ...interface{}) *HTTPError {
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
	e.Description = fmt.Sprintf(tr.Trans("desc."+extCode.String()), params...)
	return e
}

// Error returns error message.
func (e *HTTPError) Error() string {
	errBys, _ := jsoniter.Marshal(e)
	return string(errBys)
}

var (
	errServerName = "AgentOperatorIntegration"

	errCodeMap = map[int]string{
		http.StatusBadRequest:          "BadRequest",
		http.StatusUnauthorized:        "Unauthorized",
		http.StatusForbidden:           "Forbidden",
		http.StatusNotFound:            "NotFound",
		http.StatusMethodNotAllowed:    "MethodNotAllowed",
		http.StatusConflict:            "Conflict",
		http.StatusInternalServerError: "InternalServerError",
		http.StatusNotImplemented:      "NotImplemented",
		http.StatusServiceUnavailable:  "ServiceUnavailable",
		http.StatusRequestTimeout:      "RequestTimeout",
		http.StatusGatewayTimeout:      "GatewayTimeout",
	}
)

// DefaultHTTPError public error code.
func DefaultHTTPError(ctx context.Context, httpCode int, details interface{}) *HTTPError {
	language := common.GetLanguageFromCtx(ctx)
	tr := localize.NewI18nTranslator(language)
	errCode := errCodeMap[httpCode]
	if errCode == "" {
		errCode = errCodeMap[http.StatusInternalServerError]
	}
	// Get solution and error link with default values.
	solutionKey := "sol." + errCode
	solution := tr.Trans(solutionKey)
	if solution == solutionKey { // Fallback to the general solution when no corresponding translation is found.
		solution = tr.Trans("sol.Common")
	}

	errorLinkKey := "link." + errCode
	errorLink := tr.Trans(errorLinkKey)
	if errorLink == errorLinkKey { // Returns "None" when no corresponding translation is found.
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

// NewHTTPError creates HTTPError @extCode: extended error code.
func NewHTTPError(ctx context.Context, httpCode int, extCode ErrorCode, details interface{}, descParams ...interface{}) *HTTPError {
	language := common.GetLanguageFromCtx(ctx)
	tr := localize.NewI18nTranslator(language)
	errCode := errCodeMap[httpCode]
	if errCode == "" {
		errCode = errCodeMap[http.StatusInternalServerError]
	}
	// Get solution and error link with default values.
	solutionKey := "sol." + extCode.String()
	solution := tr.Trans(solutionKey)
	if solution == solutionKey { // Fallback to the general solution when no corresponding translation is found.
		solution = tr.Trans("sol.Common")
	}

	errorLinkKey := "link." + extCode.String()
	errorLink := tr.Trans(errorLinkKey)
	if errorLink == errorLinkKey { // Returns "None" when no corresponding translation is found.
		errorLink = tr.Trans("link.None")
	}
	return &HTTPError{
		HTTPCode:     httpCode,
		Language:     language,
		Code:         fmt.Sprintf("%s.%s.%s", errServerName, errCode, extCode),
		Description:  fmt.Sprintf(tr.Trans("desc."+extCode.String()), descParams...), // Can handle formatting placeholders such as %d, %s, and %v.
		Solution:     solution,
		ErrorLink:    errorLink,
		ErrorDetails: details,
	}
}
