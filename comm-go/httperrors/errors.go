// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package httperrors

import (
	"context"
	"fmt"
	"maps"

	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

type BaseError struct {
	ErrorCode    string         `json:"error_code"`         // Stable error code.
	Description  string         `json:"description"`        // Human-readable description.
	ErrorDetails any            `json:"error_details"`      // Structured error details.
	Metadata     map[string]any `json:"metadata,omitempty"` // Public rendering parameters.
}

var registeredErrors = make(map[string]struct{})

// Register validates message ownership and records stable error codes, without
// executing templates. Call it serially during startup after loading the service
// messages and before serving requests. Runtime registration is not supported.
// Duplicate codes and missing localized resources panic without a partial registration.
func Register(errorCodes []string) {
	pending, err := validateRegistration(errorCodes)
	if err != nil {
		panic(err)
	}
	maps.Copy(registeredErrors, pending)
}

// validateRegistration validates a whole startup batch without publishing state.
func validateRegistration(errorCodes []string) (map[string]struct{}, error) {
	pending := make(map[string]struct{}, len(errorCodes))
	for _, code := range errorCodes {
		if _, exists := registeredErrors[code]; exists {
			return nil, fmt.Errorf("duplicate errorCode: %s", code)
		}
		if _, exists := pending[code]; exists {
			return nil, fmt.Errorf("duplicate errorCode: %s", code)
		}
		pending[code] = struct{}{}
	}
	if err := i18n.ValidateMessages(errorCodes...); err != nil {
		return nil, fmt.Errorf("register errorCodes: %w", err)
	}
	return pending, nil
}

type HTTPError struct {
	HTTPCode int
	Lang     i18n.Language
	BaseError
}

// NewHTTPError creates an error without rendering localized text.
func NewHTTPError(ctx context.Context, httpCode int, errorCode string) *HTTPError {
	if _, exists := registeredErrors[errorCode]; !exists {
		panic("missing errorCode: " + errorCode)
	}

	lang := i18n.GetLanguageByCtx(ctx)
	return &HTTPError{
		HTTPCode: httpCode,
		Lang:     lang,
		BaseError: BaseError{
			ErrorCode:    errorCode,
			ErrorDetails: "",
		},
	}
}

// WithErrorDetails sets structured error details.
func (e *HTTPError) WithErrorDetails(errorDetails any) *HTTPError {
	e.ErrorDetails = errorDetails
	return e
}

// WithMetadata merges public rendering parameters. Later values overwrite earlier
// ones; nil/empty input is a no-op. Entries are copied into the existing map.
// Nested values are shared and must not be mutated after they are supplied.
func (e *HTTPError) WithMetadata(metadata map[string]any) *HTTPError {
	if len(metadata) == 0 {
		return e
	}
	if e.Metadata == nil {
		e.Metadata = make(map[string]any)
	}
	maps.Copy(e.Metadata, metadata)
	return e
}

// Render updates the error description without logging.
// Lookup and execution failures clear the description and return a typed rendering error.
func (e *HTTPError) Render() error {
	description, err := i18n.Render(e.Lang, e.ErrorCode, e.Metadata)
	e.Description = description
	return err
}

// MarshalJSON encodes the rendered envelope without logging. Call Render directly
// to inspect rendering failures; this method returns only JSON encoding errors.
func (e HTTPError) MarshalJSON() ([]byte, error) {
	_ = e.Render()
	return sonic.Marshal(e.BaseError)
}

func (e *HTTPError) Error() string {
	data, err := e.MarshalJSON()
	if err != nil {
		logger.Errorf("serialize HTTP error failed: error_code=%s", e.ErrorCode)
		return e.ErrorCode
	}
	return string(data)
}

func (e *BaseError) Error() string {
	data, err := sonic.Marshal(e)
	if err != nil {
		logger.Errorf("serialize base error failed: error_code=%s", e.ErrorCode)
		return e.ErrorCode
	}
	return string(data)
}
