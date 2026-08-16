// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"
)

// VegaDownstreamError preserves the status code and error body returned by vega-backend.
//
// Keeping the structured response lets callers distinguish request errors from dependency
// failures instead of mapping every downstream failure to HTTP 500.
type VegaDownstreamError struct {
	StatusCode  int
	ErrorCode   string
	Description string
	Details     string
	Raw         string
}

func (e *VegaDownstreamError) Error() string {
	msg := fmt.Sprintf("vega-backend responded %d", e.StatusCode)
	if e.ErrorCode != "" {
		msg += ": " + e.ErrorCode
	}
	if detail := e.Message(); detail != "" {
		msg += ": " + detail
	}
	return msg
}

// maxRawMessageLen limits the raw response used as a fallback message.
//
// Some gateway errors return an entire HTML page. Truncation preserves enough information to
// identify the source without flooding the client-facing error_details field.
const maxRawMessageLen = 512

// Message returns the most specific downstream message: error_details, description, then raw body.
func (e *VegaDownstreamError) Message() string {
	if e.Details != "" {
		return e.Details
	}
	if e.Description != "" {
		return e.Description
	}
	if len(e.Raw) <= maxRawMessageLen {
		return e.Raw
	}
	// Avoid leaving an incomplete UTF-8 sequence after byte-based truncation.
	truncated := e.Raw[:maxRawMessageLen]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + "...(truncated)"
}

// IsClientError reports whether the caller can fix the failure by changing the request.
func (e *VegaDownstreamError) IsClientError() bool {
	return e.StatusCode >= http.StatusBadRequest && e.StatusCode < http.StatusInternalServerError
}

// AsVegaDownstreamError extracts VegaDownstreamError from an error chain.
func AsVegaDownstreamError(err error) (*VegaDownstreamError, bool) {
	var target *VegaDownstreamError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// NewVegaDownstreamError parses a vega-backend error response. Unknown formats remain in Raw;
// the status code still provides enough information for classification.
func NewVegaDownstreamError(statusCode int, raw string) *VegaDownstreamError {
	de := &VegaDownstreamError{StatusCode: statusCode, Raw: raw}

	var payload struct {
		ErrorCode    string `json:"error_code"`
		Description  string `json:"description"`
		ErrorDetails string `json:"error_details"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err == nil {
		de.ErrorCode = payload.ErrorCode
		de.Description = payload.Description
		de.Details = payload.ErrorDetails
	}
	return de
}
