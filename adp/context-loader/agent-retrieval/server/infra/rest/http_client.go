// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/bytedance/sonic"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// httpClient HTTP client structure.
type httpClient struct {
	client *http.Client
	logger interfaces.Logger
}

// HTTPClientOptions configuration information.
type HTTPClientOptions struct {
	TimeOut int
}

// NewRawHTTPClient creates a native HTTP client object.
func NewRawHTTPClient() *http.Client {
	opts := HTTPClientOptions{
		TimeOut: 600, //nolint:mnd
	}
	return NewRawHTTPClientWithOptions(opts)
}

// NewHTTPClientWithOptions creates an HTTP client object based on configuration.
func NewHTTPClientWithOptions(opts HTTPClientOptions) interfaces.HTTPClient {
	client := &httpClient{
		client: NewRawHTTPClientWithOptions(opts),
		logger: logger.DefaultLogger(),
	}

	return client
}

// NewRawHTTPClientWithOptions creates a native HTTP client object based on configuration.
func NewRawHTTPClientWithOptions(opts HTTPClientOptions) *http.Client {
	rawClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			MaxIdleConnsPerHost:   100,              //nolint:mnd
			MaxIdleConns:          100,              //nolint:mnd
			IdleConnTimeout:       90 * time.Second, //nolint:mnd
			TLSHandshakeTimeout:   10 * time.Second, //nolint:mnd
			ExpectContinueTimeout: 30 * time.Second, //nolint:mnd
			DisableKeepAlives:     false,
		},
		Timeout: time.Duration(opts.TimeOut) * time.Second,
	}

	return rawClient
}

func NewHTTPClientWithRawClient(rawClient *http.Client) *httpClient {
	client := &httpClient{
		client: rawClient,
	}

	return client
}

// NewHTTPClient creates an HTTP client object.
func NewHTTPClient() interfaces.HTTPClient {
	client := &httpClient{
		client: NewRawHTTPClient(),
		logger: logger.DefaultLogger(),
	}

	return client
}

// Get, returns the serialized object.
func (c *httpClient) Get(ctx context.Context, rawURL string, queryValues url.Values, headers map[string]string) (respCode int, respData interface{}, err error) {
	url, err := c.generateURL(rawURL, queryValues)
	if err != nil {
		c.logger.Error(err.Error())
		return
	}

	return c.httpDo(ctx, http.MethodGet, url.String(), headers, nil)
}

// Get, returntext.
func (c *httpClient) GetNoUnmarshal(ctx context.Context, rawURL string, queryValues url.Values, headers map[string]string) (respCode int, respBody []byte, err error) {
	url, err := c.generateURL(rawURL, queryValues)
	if err != nil {
		c.logger.Error(err.Error())
		return
	}

	return c.httpDoNoUnmarshal(ctx, http.MethodGet, url.String(), headers, nil)
}

// Post, pass in the serialized object and return the serialized object.
func (c *httpClient) Post(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respData interface{}, err error) {
	return c.httpDo(ctx, http.MethodPost, url, headers, reqParam)
}

// Post, pass in the serialized object and return text.
func (c *httpClient) PostNoUnmarshal(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respBody []byte, err error) {
	return c.httpDoNoUnmarshal(ctx, http.MethodPost, url, headers, reqParam)
}

// Post, pass in the serialized object and return the raw body, status-checked.
func (c *httpClient) PostBytes(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respBody []byte, err error) {
	return c.httpDoBytes(ctx, http.MethodPost, url, headers, reqParam)
}

// Put, pass in the serialized object and return the serialized object.
func (c *httpClient) Put(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respData interface{}, err error) {
	return c.httpDo(ctx, http.MethodPut, url, headers, reqParam)
}

// Put, pass in the serialized object and return text.
func (c *httpClient) PutNoUnmarshal(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respBody []byte, err error) {
	return c.httpDoNoUnmarshal(ctx, http.MethodPut, url, headers, reqParam)
}

// Delete, returns the serialized object.
func (c *httpClient) Delete(ctx context.Context, url string, headers map[string]string) (respCode int, respData interface{}, err error) {
	return c.httpDo(ctx, http.MethodDelete, url, headers, nil)
}

// Delete, pass in the serialized object and return text.
func (c *httpClient) DeleteNoUnmarshal(ctx context.Context, url string, headers map[string]string) (respCode int, respBody []byte, err error) {
	return c.httpDoNoUnmarshal(ctx, http.MethodDelete, url, headers, nil)
}

// Patch, pass in the serialized object and return the serialized object.
func (c *httpClient) Patch(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respData interface{}, err error) {
	return c.httpDo(ctx, http.MethodPatch, url, headers, reqParam)
}

// Patch, pass in the serialized object and return text.
func (c *httpClient) PatchNoUnmarshal(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respBody []byte, err error) {
	return c.httpDoNoUnmarshal(ctx, http.MethodPatch, url, headers, reqParam)
}

// Get, return the raw body, status-checked.
func (c *httpClient) GetBytes(ctx context.Context, rawURL string, queryValues url.Values,
	headers map[string]string,
) (respCode int, respBody []byte, err error) {
	uri, err := c.generateURL(rawURL, queryValues)
	if err != nil {
		return 0, nil, err
	}
	return c.httpDoBytes(ctx, http.MethodGet, uri.String(), headers, nil)
}

// Deserialize return content.
func (c *httpClient) httpDo(ctx context.Context, mtehod, url string, headers map[string]string, reqParam interface{}) (respCode int, respData interface{}, err error) {
	respCode, respBody, err := c.httpDoNoUnmarshal(ctx, mtehod, url, headers, reqParam)
	if err != nil {
		c.logger.Error(err.Error())
		return
	}
	if len(respBody) == 0 {
		return
	}
	err = sonic.Unmarshal(respBody, &respData)
	if err != nil {
		c.logger.Error(err.Error())
		respData = string(respBody)
	}
	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		respStr := utils.ObjectToJSON(respData)
		c.logger.Errorf("Exception(http do error, method: %s, url: %s, headers: %v, reqParam: %v, respCode: %d, error: %s)",
			mtehod, url, utils.ObjectToJSON(headers), utils.ObjectToJSON(reqParam), respCode, respStr)
		// Exception when calling external service.
		err = infraErr.NewHTTPError(ctx, respCode, infraErr.ErrExtCommonExternalServerError,
			fmt.Sprintf("Exception(http do error, method: %s, url: %s,  http status: %d, error: %s)", mtehod, url, respCode, respStr))
		return
	}
	return
}

// httpDoBytes is httpDo without the deserialization: same status checking and
// same error type, but the body is handed back as raw JSON so the caller can
// decode it itself.
//
// httpDo cannot be used for responses that carry business data. It unmarshals
// into an interface{}, where sonic's default configuration turns every JSON
// number into a float64 — anything past float64's 53-bit mantissa is silently
// rounded, and 9223372036854775808 re-serializes as 9.223372036854776e+18.
// Object instance properties are arbitrary business data (MySQL BIGINT /
// BIGINT UNSIGNED columns, ID card numbers, snowflake IDs), so the rounding is
// not hypothetical: see openbkn-ai/bkn-studio#464.
//
// Non-2xx handling is deliberately identical to httpDo — callers such as
// ontology_query.classifyQueryError match on the *infraErr.HTTPError it
// produces, so this must keep returning the same error type carrying the same
// HTTP code.
func (c *httpClient) httpDoBytes(ctx context.Context, method, url string, headers map[string]string,
	reqParam interface{},
) (respCode int, respBody []byte, err error) {
	respCode, respBody, err = c.httpDoNoUnmarshal(ctx, method, url, headers, reqParam)
	if err != nil {
		c.logger.Error(err.Error())
		return
	}
	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		respStr := string(respBody)
		c.logger.Errorf("Exception(http do error, method: %s, url: %s, headers: %v, reqParam: %v, respCode: %d, error: %s)",
			method, url, utils.ObjectToJSON(headers), utils.ObjectToJSON(reqParam), respCode, respStr)
		// Exception when calling external service.
		err = infraErr.NewHTTPError(ctx, respCode, infraErr.ErrExtCommonExternalServerError,
			fmt.Sprintf("Exception(http do error, method: %s, url: %s,  http status: %d, error: %s)", method, url, respCode, respStr))
		return
	}
	return
}

// Return original respBody without deserialization.
func (c *httpClient) httpDoNoUnmarshal(ctx context.Context, mtehod, url string, headers map[string]string, reqParam interface{}) (respCode int, respBody []byte, err error) {
	if c.client == nil {
		return 0, nil, errors.New("http client is unavailable")
	}

	req, err := c.generateReq(ctx, mtehod, url, headers, reqParam)
	if err != nil {
		c.logger.Error(err.Error())
		return 0, nil, err
	}

	resp, err := telemetry.HTTPRequest(ctx, req, func(req *http.Request) (rsp *http.Response, err error) {
		return c.client.Do(req)
	})
	if err != nil {
		c.logger.Error(err.Error())
		return
	}
	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			c.logger.Error(closeErr.Error())
		}
	}()
	respBody, err = io.ReadAll(resp.Body)
	respCode = resp.StatusCode
	return
}

func (c *httpClient) generateURL(rawURL string, queryValues url.Values) (*url.URL, error) {
	uri, err := url.Parse(rawURL)
	if err != nil {
		c.logger.Error(err.Error())
		return nil, err
	}

	if queryValues != nil {
		values := uri.Query()
		for k, v := range values {
			queryValues[k] = v
		}
		uri.RawQuery = queryValues.Encode()
	}

	return uri, err
}

func (c *httpClient) generateReq(ctx context.Context, httpMethod, url string,
	headers map[string]string, reqParam interface{},
) (req *http.Request, err error) {
	if reqParam != nil {
		var reader *bytes.Reader
		if v, ok := reqParam.([]byte); ok {
			reader = bytes.NewReader(v)
		} else {
			reqData, err := sonic.Marshal(reqParam)
			if err != nil {
				c.logger.Error(err.Error())
				return nil, err
			}
			reader = bytes.NewReader(reqData)
		}
		req, err = http.NewRequestWithContext(ctx, httpMethod, url, reader)
	} else {
		req, err = http.NewRequestWithContext(ctx, httpMethod, url, http.NoBody)
	}

	if err != nil {
		c.logger.Error(err.Error())
		return
	}

	for k, v := range headers {
		if v != "" {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set(sharedrest.AcceptLanguageHeader, sharedrest.GetLanguageByCtx(ctx))
	return
}
