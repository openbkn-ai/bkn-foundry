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
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/bytedance/sonic"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

//go:generate mockgen -package mock -source ./http_client.go -destination ./mock/mock_http_client.go

// HTTPClient defines the HTTP client service interface.
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
}

// httpClient implements HTTPClient.
type httpClient struct {
	client *http.Client
}

// HttpClientOptions configures an HTTP client.
type HttpClientOptions struct {
	TimeOut int
}

// NewRawHTTPClient creates a raw HTTP client.
func NewRawHTTPClient() *http.Client {
	opts := HttpClientOptions{
		TimeOut: 600,
	}
	return NewRawHTTPClientWithOptions(opts)
}

// NewHTTPClientWithOptions creates an HTTP client with the supplied options.
func NewHTTPClientWithOptions(opts HttpClientOptions) HTTPClient {
	client := &httpClient{
		client: NewRawHTTPClientWithOptions(opts),
	}

	return client
}

// NewRawHTTPClientWithOptions creates a raw HTTP client with the supplied options.
func NewRawHTTPClientWithOptions(opts HttpClientOptions) *http.Client {
	rawClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			MaxIdleConnsPerHost:   100,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 30 * time.Second,
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

// NewHTTPClient creates an HTTP client.
func NewHTTPClient() HTTPClient {
	client := &httpClient{
		client: NewRawHTTPClient(),
	}

	return client
}

// Get returns a decoded response object.
func (c *httpClient) Get(ctx context.Context, rawURL string, queryValues url.Values, headers map[string]string) (respCode int, respData interface{}, err error) {
	url, err := c.generateURL(rawURL, queryValues)
	if err != nil {
		logger.Error(err.Error())
		return
	}

	return c.httpDo(ctx, http.MethodGet, url.String(), headers, nil)
}

// GetNoUnmarshal returns the raw response body.
func (c *httpClient) GetNoUnmarshal(ctx context.Context, rawURL string, queryValues url.Values, headers map[string]string) (respCode int, respBody []byte, err error) {
	url, err := c.generateURL(rawURL, queryValues)
	if err != nil {
		logger.Error(err.Error())
		return
	}

	return c.httpDoNoUnmarshal(ctx, http.MethodGet, url.String(), headers, nil)
}

// Post sends a serializable request and returns a decoded response object.
func (c *httpClient) Post(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respData interface{}, err error) {
	return c.httpDo(ctx, http.MethodPost, url, headers, reqParam)
}

// PostNoUnmarshal sends a serializable request and returns the raw response body.
func (c *httpClient) PostNoUnmarshal(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respBody []byte, err error) {
	return c.httpDoNoUnmarshal(ctx, http.MethodPost, url, headers, reqParam)
}

// Put sends a serializable request and returns a decoded response object.
func (c *httpClient) Put(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respData interface{}, err error) {
	return c.httpDo(ctx, http.MethodPut, url, headers, reqParam)
}

// PutNoUnmarshal sends a serializable request and returns the raw response body.
func (c *httpClient) PutNoUnmarshal(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respBody []byte, err error) {
	return c.httpDoNoUnmarshal(ctx, http.MethodPut, url, headers, reqParam)
}

// Delete returns a decoded response object.
func (c *httpClient) Delete(ctx context.Context, url string, headers map[string]string) (respCode int, respData interface{}, err error) {
	return c.httpDo(ctx, http.MethodDelete, url, headers, nil)
}

// DeleteNoUnmarshal returns the raw response body.
func (c *httpClient) DeleteNoUnmarshal(ctx context.Context, url string, headers map[string]string) (respCode int, respBody []byte, err error) {
	return c.httpDoNoUnmarshal(ctx, http.MethodDelete, url, headers, nil)
}

// Patch sends a serializable request and returns a decoded response object.
func (c *httpClient) Patch(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respData interface{}, err error) {
	return c.httpDo(ctx, http.MethodPatch, url, headers, reqParam)
}

// PatchNoUnmarshal sends a serializable request and returns the raw response body.
func (c *httpClient) PatchNoUnmarshal(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (respCode int, respBody []byte, err error) {
	return c.httpDoNoUnmarshal(ctx, http.MethodPatch, url, headers, reqParam)
}

// httpDo decodes the response body.
func (c *httpClient) httpDo(ctx context.Context, mtehod string, url string, headers map[string]string,
	reqParam interface{}) (respCode int, respData interface{}, err error) {

	respCode, respBody, err := c.httpDoNoUnmarshal(ctx, mtehod, url, headers, reqParam)
	if err != nil {
		logger.Error(err.Error())
		return
	}
	if len(respBody) == 0 {
		return
	}

	err = sonic.Unmarshal(respBody, &respData)
	if err != nil {
		logger.Error(err.Error())
	}
	return
}

// httpDoNoUnmarshal returns the raw response body without decoding it.
func (c *httpClient) httpDoNoUnmarshal(ctx context.Context, mtehod string, url string, headers map[string]string,
	reqParam interface{}) (respCode int, respBody []byte, err error) {

	if c.client == nil {
		return 0, nil, errors.New("http client is unavailable")
	}

	req, err := c.generateReq(ctx, mtehod, url, headers, reqParam)
	if err != nil {
		logger.Error(err.Error())
		return 0, nil, err
	}

	// Inject the trace context into request headers.
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := c.client.Do(req)
	if err != nil {
		logger.Error(err.Error())
		return
	}
	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			logger.Error(closeErr.Error())
		}
	}()
	respBody, err = io.ReadAll(resp.Body)
	respCode = resp.StatusCode
	return
}

func (c *httpClient) generateURL(rawURL string, queryValues url.Values) (*url.URL, error) {
	uri, err := url.Parse(rawURL)
	if err != nil {
		logger.Error(err.Error())
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

func (c *httpClient) generateReq(ctx context.Context, httpMethod string, url string, headers map[string]string, reqParam interface{}) (req *http.Request, err error) {
	if reqParam != nil {
		var reader *bytes.Reader
		if v, ok := reqParam.([]byte); ok {
			reader = bytes.NewReader(v)
		} else {
			reqData, err := sonic.Marshal(reqParam)
			if err != nil {
				logger.Error(err.Error())
				return nil, err
			}
			reader = bytes.NewReader(reqData)
		}
		req, err = http.NewRequestWithContext(ctx, httpMethod, url, reader)
	} else {
		req, err = http.NewRequestWithContext(ctx, httpMethod, url, nil)
	}

	if err != nil {
		logger.Error(err.Error())
		return
	}

	for k, v := range headers {
		if len(v) > 0 {
			req.Header.Add(k, v)
		}
	}
	// Service-to-service calls use the resolved locale, not the original range.
	req.Header.Set(AcceptLanguageHeader, GetLanguageByCtx(ctx))
	return
}
