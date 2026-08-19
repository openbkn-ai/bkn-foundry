package rest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// httpClient HTTP client structure.
type httpClient struct {
	client *http.Client
	logger interfaces.Logger
}

// HTTPClientOptions configuration information.
type HTTPClientOptions struct {
	TimeOut               int
	ResponseHeaderTimeout int
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
		// Disable automatic jump.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		// Custom Transport.
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			MaxIdleConnsPerHost:   100,              //nolint:mnd
			MaxIdleConns:          100,              //nolint:mnd
			IdleConnTimeout:       90 * time.Second, //nolint:mnd
			TLSHandshakeTimeout:   10 * time.Second, //nolint:mnd
			ExpectContinueTimeout: 30 * time.Second, //nolint:mnd
			DisableKeepAlives:     false,
			ResponseHeaderTimeout: time.Duration(opts.ResponseHeaderTimeout) * time.Second,
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

// Get, return text.
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
	if (respCode < http.StatusOK) || (respCode >= http.StatusMultipleChoices) {
		err = &ExHTTPError{
			Body:     respBody,
			HTTPCode: respCode,
		}
		return
	}
	err = sonic.Unmarshal(respBody, &respData)
	if err != nil {
		c.logger.Error(err.Error())
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
	headers map[string]string, reqParam interface{}) (req *http.Request, err error) {
	if reqParam != nil {
		var reader *bytes.Reader
		if v, ok := reqParam.([]byte); ok {
			reader = bytes.NewReader(v)
		} else {
			var reqData []byte
			reqData, err = sonic.Marshal(reqParam)
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

// PostStream sends a POST request and returns a streaming response.
func (c *httpClient) PostStream(ctx context.Context, url string, headers map[string]string, reqParam interface{}) (chan string, chan error, error) {
	messages := make(chan string)
	errs := make(chan error)
	go func() {
		defer close(messages)
		defer close(errs)

		var read *bytes.Reader
		if v, ok := reqParam.([]byte); ok {
			read = bytes.NewReader(v)
		} else {
			var reqData []byte
			reqData, err := sonic.Marshal(reqParam)
			if err != nil {
				errs <- err
				return
			}
			read = bytes.NewReader(reqData)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, read)
		if err != nil {
			errs <- err
			return
		}

		for k, v := range headers {
			req.Header.Add(k, v)
		}
		req.Header.Set(sharedrest.AcceptLanguageHeader, sharedrest.GetLanguageByCtx(ctx))
		// Set streaming request headers.
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Connection", "keep-alive")

		resp, err := c.client.Do(req)
		if err != nil {
			errs <- err
			return
		}
		defer func() {
			if resp != nil && resp.Body != nil {
				if closeErr := resp.Body.Close(); closeErr != nil {
					errs <- closeErr
				}
			}
		}()

		if resp.StatusCode != http.StatusOK {
			// Read response body.
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				errs <- err
				return
			}
			errs <- fmt.Errorf("POST request failed with status %d: %s", resp.StatusCode, string(body))
			return
		}
		// Handling streaming responses.
		reader := bufio.NewReader(resp.Body)

		var currentEvent strings.Builder

		for {
			line, isPrefix, err := reader.ReadLine()
			if err != nil {
				if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
					return
				}
				errs <- err
				return
			}

			// Handle long lines (isPrefix is true)
			if isPrefix {
				// For long lines, continue reading until the complete line.
				currentEvent.Write(line)
				continue
			}

			// Complete line, forward directly.
			lineStr := string(line)
			if lineStr != "" {
				currentEvent.WriteString(lineStr)
				messages <- currentEvent.String()
				currentEvent.Reset()
			}
		}
	}()
	return messages, errs, nil
}
