// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// HTTPClient wraps HTTP requests for tests.
type HTTPClient struct {
	BaseURL string
	Client  *http.Client
	Headers map[string]string // Common headers such as X-Account-ID.
}

// HTTPResponse wraps an HTTP response.
type HTTPResponse struct {
	StatusCode int
	Headers    http.Header    // Response headers.
	Body       map[string]any // Successful response JSON body.
	Error      *ErrorResponse // Error response.
	RawBody    []byte         // Raw response body.
}

// ErrorResponse is the error response structure.
type ErrorResponse struct {
	ErrorCode    string `json:"error_code"`
	ErrorDetails string `json:"error_details"`
}

// NewHTTPClient creates a new HTTP test client.
func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		BaseURL: baseURL,
		Client:  &http.Client{Timeout: 120 * time.Second},
		Headers: map[string]string{
			"Content-Type":      "application/json",
			"X-Account-ID":      "test-user-001",
			"X-Account-Type":    "user",
			"x-business-domain": "test-domain",
		},
	}
}

// CheckHealth checks service health.
func (c *HTTPClient) CheckHealth() error {
	resp, err := c.Client.Get(c.BaseURL + "/health")
	if err != nil {
		return fmt.Errorf("健康检查失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("健康检查返回状态码 %d", resp.StatusCode)
	}
	return nil
}

// POST sends a POST request.
func (c *HTTPClient) POST(path string, payload any) HTTPResponse {
	return c.doRequest("POST", path, payload)
}

// GET sends a GET request.
func (c *HTTPClient) GET(path string) HTTPResponse {
	return c.doRequest("GET", path, nil)
}

// PUT sends a PUT request.
func (c *HTTPClient) PUT(path string, payload any) HTTPResponse {
	return c.doRequest("PUT", path, payload)
}

// DELETE sends a DELETE request.
func (c *HTTPClient) DELETE(path string) HTTPResponse {
	return c.doRequest("DELETE", path, nil)
}

// POSTMultipart sends a multipart/form-data POST request for file uploads.
func (c *HTTPClient) POSTMultipart(path string, fileFieldName string, fileContent []byte, fileName string, extraParams map[string]string) HTTPResponse {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Add the file.
	part, err := writer.CreateFormFile(fileFieldName, fileName)
	if err != nil {
		return HTTPResponse{
			StatusCode: 0,
			Error:      &ErrorResponse{ErrorCode: "create_form_error", ErrorDetails: err.Error()},
		}
	}
	_, err = part.Write(fileContent)
	if err != nil {
		return HTTPResponse{
			StatusCode: 0,
			Error:      &ErrorResponse{ErrorCode: "write_file_error", ErrorDetails: err.Error()},
		}
	}

	// Add extra parameters.
	for key, val := range extraParams {
		_ = writer.WriteField(key, val)
	}

	err = writer.Close()
	if err != nil {
		return HTTPResponse{
			StatusCode: 0,
			Error:      &ErrorResponse{ErrorCode: "close_writer_error", ErrorDetails: err.Error()},
		}
	}

	// Build the full URL.
	url := c.BaseURL + path

	// Create the request.
	req, err := http.NewRequest("POST", url, &body)
	if err != nil {
		return HTTPResponse{
			StatusCode: 0,
			Error:      &ErrorResponse{ErrorCode: "create_request_error", ErrorDetails: err.Error()},
		}
	}

	// Set headers.
	for key, value := range c.Headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send the request.
	resp, err := c.Client.Do(req)
	if err != nil {
		return HTTPResponse{
			StatusCode: 0,
			Error:      &ErrorResponse{ErrorCode: "request_error", ErrorDetails: err.Error()},
		}
	}
	defer resp.Body.Close()

	// Read the response body.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return HTTPResponse{
			StatusCode: resp.StatusCode,
			Error:      &ErrorResponse{ErrorCode: "read_body_error", ErrorDetails: err.Error()},
		}
	}

	// Parse the response.
	result := HTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		RawBody:    respBody,
	}

	// Try to parse JSON.
	if len(respBody) > 0 {
		var jsonBody map[string]any
		if err := json.Unmarshal(respBody, &jsonBody); err == nil {
			result.Body = jsonBody
		}
	}

	return result
}

// doRequest executes an HTTP request internally.
func (c *HTTPClient) doRequest(method, path string, payload any) HTTPResponse {
	var bodyReader io.Reader
	if payload != nil {
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return HTTPResponse{
				StatusCode: 0,
				Error:      &ErrorResponse{ErrorCode: "marshal_error", ErrorDetails: err.Error()},
			}
		}
		bodyReader = bytes.NewBuffer(bodyBytes)
	}

	// Build the full URL.
	url := c.BaseURL + path

	// Create the request.
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return HTTPResponse{
			StatusCode: 0,
			Error:      &ErrorResponse{ErrorCode: "create_request_error", ErrorDetails: err.Error()},
		}
	}

	// Set headers.
	for key, value := range c.Headers {
		req.Header.Set(key, value)
	}

	// Send the request.
	resp, err := c.Client.Do(req)
	if err != nil {
		return HTTPResponse{
			StatusCode: 0,
			Error:      &ErrorResponse{ErrorCode: "request_error", ErrorDetails: err.Error()},
		}
	}
	defer resp.Body.Close()

	// Read the response body.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return HTTPResponse{
			StatusCode: resp.StatusCode,
			Error:      &ErrorResponse{ErrorCode: "read_body_error", ErrorDetails: err.Error()},
		}
	}

	// Parse the response.
	result := HTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		RawBody:    respBody,
	}

	// Try to parse JSON.
	if len(respBody) > 0 {
		var jsonBody map[string]any
		if err := json.Unmarshal(respBody, &jsonBody); err == nil {
			result.Body = jsonBody
		} else {
			// If it is not JSON, keep the original content.
			result.Body = map[string]any{"raw": string(respBody)}
		}
	}

	return result
}
