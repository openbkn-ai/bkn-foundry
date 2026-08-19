package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	myErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/pkg/errors"
)

// Forwarder HTTP request forwarder interface.
type Forwarder interface {
	Forward(ctx context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error)
	ForwardStream(ctx context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error)
}

// forwarder HTTP request forwarder.
type forwarder struct {
	pool            *clientPool
	streamProcessor *StreamProcessor
	logger          interfaces.Logger
}

var (
	forwarderOnce sync.Once
	f             Forwarder
)

// NewForwarder creates a new HTTP request forwarder.
func NewForwarder() Forwarder {
	forwarderOnce.Do(func() {
		logger := config.NewConfigLoader().GetLogger()
		f = &forwarder{
			pool:            NewClientPool(),
			streamProcessor: NewStreamProcessor(logger),
			logger:          logger,
		}
	})
	return f
}

// HTTPStreamForward handles HTTP streaming requests.
func (f *forwarder) ForwardStream(ctx context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error) {
	startTime := time.Now()
	// Verify request parameters.
	streamingMode, ok := common.GetStreamingModeFromCtx(ctx)
	if !ok {
		streamingMode = interfaces.StreamingModeHTTP
	}
	// Get response writer.
	headerWriter, ok := common.GetResponseWriterFromCtx(ctx)
	if !ok {
		err := fmt.Errorf("response writer not found in context")
		f.logger.WithContext(ctx).Warnf("failed to forward stream, err: %v", err)
		err = myErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return nil, err
	}
	httpReq, err := f.buildRequest(ctx, req)
	if err != nil {
		headerWriter.WriteHeader(http.StatusInternalServerError)
		f.logger.WithContext(ctx).Warnf("build request failed, err: %v", err)
		err = myErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return nil, err
	}
	// Create a client without timeout for streaming requests.
	streamClient := f.pool.GetStreamClient(streamingMode, req.Timeout)

	// New Added: Set necessary request headers for streaming requests.
	prepareStreamRequest(streamingMode, httpReq)
	now := time.Now()
	f.logger.Debugf("do stream request, streamingMode: %v, timeout: %v", streamingMode, req.Timeout)
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		// On the streaming server, set the ResponseHeaderTimeout parameter to 10s. If no request header is returned within 10s, it is considered a timeout.
		// If the server does not support streaming requests, but the client uses a streaming proxy, it is also considered a timeout.
		// Timeout error example: net/http: timeout awaiting response headers".
		headerWriter.WriteHeader(http.StatusRequestTimeout)
		if strings.Contains(err.Error(), "timeout awaiting response headers") {
			err = errors.Wrapf(err, "The server may not support streaming requests, or the server response timed out with no response headers received within 10 seconds")
			err = myErr.DefaultHTTPError(ctx, http.StatusRequestTimeout, err.Error())
		} else {
			// The request forwarding failed and the server is reported to be unavailable. Please check whether it is available or try again later.
			err = errors.Wrapf(err, "Request forwarding failed, please check if the request is correct, or try again later")
			err = myErr.NewHTTPError(ctx, http.StatusServiceUnavailable, myErr.ErrExtProxyForwardFailed, err.Error())
		}
		f.logger.WithContext(ctx).Warnf("do request failed, err: %v, cost: %v", err, time.Since(now))
		return nil, err
	}
	f.logger.Debugf("do stream request success, streamingMode: %v, timeout: %v, cost: %v", streamingMode, req.Timeout, time.Since(now))
	defer func() {
		_ = resp.Body.Close()
	}()
	// Copy response headers to original response.
	var isSSE bool
	headers := make(map[string]any)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
			if key == "Content-Type" && strings.HasPrefix(values[0], "text/event-stream") {
				isSSE = true
			}
			headerWriter.Header().Set(key, values[0])
		}
	}
	preprocessResponseHeaders(streamingMode, headerWriter)
	// Make sure you set the correct status code.
	headerWriter.WriteHeader(resp.StatusCode)
	// Add this line of code to ensure the response headers are sent immediately.
	if flusher, ok := headerWriter.(http.Flusher); ok {
		flusher.Flush()
	}
	// Processed according to streaming mode.
	switch streamingMode {
	case interfaces.StreamingModeSSE:
		err = f.streamProcessor.ProcessSSE(ctx, resp.Body, headerWriter, isSSE)
	case interfaces.StreamingModeHTTP:
		err = f.streamProcessor.ProcessHTTPStream(ctx, resp.Body, headerWriter)
	default:
		_, err = io.Copy(headerWriter, resp.Body)
	}
	if err != nil {
		err = fmt.Errorf("failed to forward stream: %v", err)
		f.logger.WithContext(ctx).Warnf("failed to forward stream, err: %v", err)
		err = myErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return nil, err
	}
	return &interfaces.HTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    headers,
		Duration:   time.Since(startTime).Milliseconds(),
	}, nil
}

// Forward forward HTTP request.
func (f *forwarder) Forward(ctx context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error) {
	startTime := time.Now()

	// Get HTTP client.
	client := f.pool.GetClient(req.Timeout)

	// Build an HTTP request.
	httpReq, err := f.buildRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	// Send request.
	resp, err := client.Do(httpReq)
	if err != nil {
		return &interfaces.HTTPResponse{
			StatusCode: http.StatusInternalServerError,
			Error:      err.Error(),
			Duration:   time.Since(startTime).Milliseconds(),
		}, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Handle response.
	return f.processResponse(resp, startTime)
}

// buildRequest Builds an HTTP request based on request parameters.
func (f *forwarder) buildRequest(ctx context.Context, req *interfaces.HTTPRequest) (*http.Request, error) {
	// Handle URL and path parameters.
	requestURL := substitutePathParams(req.URL, req.PathParams)
	// If the path template still has unreplaced placeholders, it will be directly rejected to avoid sending invalid URLs such as "/market/{operator_id}" to downstream users.
	if unresolved := unresolvedPathPlaceholders(requestURL); len(unresolved) > 0 {
		missing := strings.Join(unresolved, ", ")
		return nil, myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtProxyPathParamMissing,
			fmt.Sprintf("unresolved path placeholder(s) [%s] in url %s", missing, requestURL), missing)
	}
	// Handle query parameters.
	if len(req.QueryParams) > 0 {
		parsedURL, err := url.Parse(requestURL)
		if err != nil {
			return nil, err
		}

		q := parsedURL.Query()
		for key, value := range req.QueryParams {
			q.Add(key, fmt.Sprintf("%v", value))
		}

		parsedURL.RawQuery = q.Encode()
		requestURL = parsedURL.String()
	}

	// Process request body.
	var reqBody io.Reader
	var contentType string
	// forceContentType means that the Content-Type recalculated during the encoding process must cover the request header with the same name passed in by the caller.
	var forceContentType bool

	// GET/HEAD does not have a request body semantically, and the debugging panel always sends "body": {} to interfaces without a body.
	// Direct processing as a normal request body will bring empty JSON body and Content-Type to the downstream, and some servers will directly reject it.
	payload := req.Body
	if isBodylessMethod(req.Method) && isEmptyBody(payload) {
		payload = nil
	}

	if payload != nil {
		// Check the Content-Type header.
		contentType = ""
		if req.Headers != nil {
			for k, v := range req.Headers {
				if strings.EqualFold(k, "content-type") {
					contentType = fmt.Sprintf("%v", v)
					break
				}
			}
		}
		// Process the request body according to Content-Type.
		switch {
		case strings.Contains(contentType, "application/json"):
			// JSON format.
			jsonData, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			reqBody = bytes.NewBuffer(jsonData)
		case strings.Contains(contentType, "application/x-www-form-urlencoded"):
			// form format.
			formData := url.Values{}

			// Try converting body to map.
			if bodyMap, ok := payload.(map[string]interface{}); ok {
				for key, value := range bodyMap {
					formData.Add(key, fmt.Sprintf("%v", value))
				}
			}
			reqBody = strings.NewReader(formData.Encode())
		case strings.Contains(contentType, "multipart/form-data"):
			// multipart form format.
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)

			// Try converting body to map.
			if bodyMap, ok := payload.(map[string]interface{}); ok {
				for key, value := range bodyMap {
					fw, err := writer.CreateFormField(key)
					if err != nil {
						return nil, err
					}
					_, err = fw.Write(utils.ObjectToByte(value))
					if err != nil {
						return nil, err
					}
				}
			}

			_ = writer.Close()
			// The Content-Type generated by the writer has boundary and must cover the caller's own multipart header, otherwise the downstream cannot parse it.
			contentType = writer.FormDataContentType()
			forceContentType = true
			reqBody = body
		case strings.Contains(contentType, "text/plain"):
			// text format.
			reqBody = strings.NewReader(fmt.Sprintf("%v", payload))
		case strings.Contains(contentType, "text/event-stream"):
			// SSE format.
			reqBody = strings.NewReader(fmt.Sprintf("%v", payload))
		case strings.Contains(contentType, "application/stream+json"), // HTTP Streaming format.
			strings.Contains(contentType, "application/x-ndjson"),      // NDJSON format.
			strings.Contains(contentType, "application/x-json-stream"): // HTTP Streaming format.
			// HTTP Streaming format.
			reqBody = strings.NewReader(fmt.Sprintf("%v", payload))
		default:
			jsonData, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			reqBody = bytes.NewBuffer(jsonData)
			if contentType == "" {
				contentType = "application/json"
			}
		}
	} else {
		reqBody = http.NoBody
	}

	// Create HTTP request.
	httpReq, err := http.NewRequest(req.Method, requestURL, reqBody)
	if err != nil {
		err = fmt.Errorf("failed to create request: %v", err)
		return nil, err
	}

	// Set the request header and complete the trace/request id with the current request context to ensure that the proxy link can be aggregated.
	// The header of the public interface is filled in directly by the caller, and the identity header must be backfilled with the authentication account of this request.
	isPublic := common.IsPublicAPIFromCtx(ctx)
	auth, _ := common.GetAccountAuthContextFromCtx(ctx)
	headers := map[string]string{}
	for key, value := range req.Headers {
		// The transport layer request header is taken over by the forwarder and Go HTTP client. Manual filling by the caller will only make the actual request and preview inconsistent.
		if isTransportHeader(key) {
			if f.logger != nil {
				f.logger.WithContext(ctx).Debugf("drop transport-layer header from caller: %s", key)
			}
			continue
		}
		if isPublic && isIdentityHeader(key) {
			resolved, ok := resolveIdentityHeader(key, auth)
			if !ok {
				if f.logger != nil {
					f.logger.WithContext(ctx).Warnf("drop identity header %s: no authenticated account in context", key)
				}
				continue
			}
			if f.logger != nil && resolved != fmt.Sprintf("%v", value) {
				f.logger.WithContext(ctx).Warnf("override caller-supplied identity header %s with authenticated account", key)
			}
			headers[key] = resolved
			continue
		}
		headers[key] = fmt.Sprintf("%v", value)
	}
	for key, value := range common.MergeTraceHeaders(ctx, headers) {
		httpReq.Header.Set(key, value)
	}
	// Internal requests carry the resolved, single-value locale rather than the
	// caller's original Accept-Language preference list.
	httpReq.Header.Set(sharedrest.AcceptLanguageHeader, sharedrest.GetLanguageByCtx(ctx))

	// If Content-Type is not set in the request header but we have a definite type, set it.
	if contentType != "" && (forceContentType || httpReq.Header.Get("Content-Type") == "") {
		httpReq.Header.Set("Content-Type", contentType)
	}

	return httpReq, nil
}

// transportHeaders The transport layer request headers taken over by the forwarding link itself will be discarded when passed in by the caller.
var transportHeaders = map[string]struct{}{
	"host":              {},
	"content-length":    {},
	"transfer-encoding": {},
	"connection":        {},
	"keep-alive":        {},
	"proxy-connection":  {},
	"te":                {},
	"trailer":           {},
	"upgrade":           {},
	"expect":            {},
}

// isTransportHeader determines whether the request header belongs to the transport layer request header (case insensitive).
func isTransportHeader(key string) bool {
	_, ok := transportHeaders[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

// identityHeaders identity request headers: the built-in toolbox declares them as ordinary OpenAPI header parameters,
// The downstream /in interface does not verify the token, but directly determines the caller's identity based on it and performs per-account authorization.
// The header of the public interface (debugging and execution) is completely filled in by the caller. Allowing transparent transmission means allowing any account to be impersonated.
// Therefore, the account authenticated by this request is backfilled here uniformly. The internal interface maintains transparent transmission, and the identity is injected according to the /in convention during runtime.
var identityHeaders = map[string]struct{}{
	"x-account-id":   {},
	"x-account-type": {},
	"user_id":        {},
	"x-user-id":      {},
}

// isIdentityHeader determines whether the request header belongs to the identity request header (case insensitive).
func isIdentityHeader(key string) bool {
	_, ok := identityHeaders[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

// resolveIdentityHeader returns the expected value of the identity request header; returning false means that the authentication account cannot be obtained, and the request header must be discarded.
func resolveIdentityHeader(key string, auth *interfaces.AccountAuthContext) (string, bool) {
	if auth == nil || auth.AccountID == "" {
		return "", false
	}
	if strings.EqualFold(strings.TrimSpace(key), "x-account-type") {
		if auth.AccountType == "" {
			return "", false
		}
		return string(auth.AccountType), true
	}
	return auth.AccountID, true
}

// bodylessMethods Methods that do not have a request body semantically.
var bodylessMethods = map[string]struct{}{
	http.MethodGet:  {},
	http.MethodHead: {},
}

// isBodylessMethod determines whether the method semantically does not have a request body (case insensitive, empty methods are treated as GET).
func isBodylessMethod(method string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(method))
	if normalized == "" {
		normalized = http.MethodGet
	}
	_, ok := bodylessMethods[normalized]
	return ok
}

// isEmptyBody determines whether the request body is an empty envelope: the debugging panel always sends "body": {} to interfaces without a body.
// Only empty values are considered as having no request body, and non-empty bodies are still encoded and sent out in the manner declared by the caller.
func isEmptyBody(body interface{}) bool {
	switch value := body.(type) {
	case nil:
		return true
	case map[string]interface{}:
		return len(value) == 0
	case map[string]string:
		return len(value) == 0
	case []interface{}:
		return len(value) == 0
	case string:
		return value == ""
	default:
		return false
	}
}

// pathPlaceholderPattern matches placeholders of the form {name} in the path.
var pathPlaceholderPattern = regexp.MustCompile(`\{([^{}/]+)\}`)

// colonParamPattern matches a placeholder of the form ":name" and requires a non-identifier character or trailing character to follow.
// Without this boundary, the parameter id will also replace the ":id" prefix in "/users/:identifier", leaving half of "entifier".
func colonParamPattern(key string) *regexp.Regexp {
	return regexp.MustCompile(`:` + regexp.QuoteMeta(key) + `([^A-Za-z0-9_]|$)`)
}

// substitutePathParams replaces the path parameter into the URL in three ways: "{name}", ":{name}", and ":name".
//
// Values are always escaped according to path segments: the path parameter in OpenAPI is a single path segment, unescaped "/", "?", "#".
// Will change the structure of the entire URL - the caller wants to pass the ID, but can rewrite the downstream request to another path, or out of thin air.
// Append query. The target URL is determined by tool metadata and cannot be changed on the debugging interface. This is the last gate.
func substitutePathParams(rawURL string, params map[string]string) string {
	if len(params) == 0 {
		return rawURL
	}

	for key, value := range params {
		escaped := url.PathEscape(value)
		// ":{key}" must be replaced before "{key}", otherwise "{key}" will be hit first and extra colons will be left in the URL.
		rawURL = strings.ReplaceAll(rawURL, ":{"+key+"}", escaped)
		rawURL = strings.ReplaceAll(rawURL, "{"+key+"}", escaped)
		// Replace with the Func version: the escaped value may contain "$" and will be treated as a grouped reference using ReplaceAllString.
		rawURL = colonParamPattern(key).ReplaceAllStringFunc(rawURL, func(match string) string {
			return escaped + match[len(key)+1:]
		})
	}

	return rawURL
}

// unresolvedPathPlaceholders Returns the placeholder names in the URL path that have not yet been replaced by the path parameter.
// Only the path part is checked: the curly braces in query and fragment may be business values, and the colon form cannot be distinguished from the port number, so it is not involved in the judgment.
func unresolvedPathPlaceholders(rawURL string) []string {
	target := rawURL
	if parsed, err := url.Parse(rawURL); err == nil {
		target = parsed.Path
	}
	matches := pathPlaceholderPattern.FindAllStringSubmatch(target, -1)
	if len(matches) == 0 {
		return nil
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match[1])
	}
	return names
}

// processResponse handles HTTP responses.
func (f *forwarder) processResponse(resp *http.Response, startTime time.Time) (*interfaces.HTTPResponse, error) {
	// Read response body.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse response headers.
	headers := make(map[string]any)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	// Try to parse JSON response.
	var responseBody interface{}
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		if err := json.Unmarshal(body, &responseBody); err != nil {
			// If parsing fails, use the original response body.
			responseBody = string(body)
		}
	} else {
		// Non-JSON response, use string.
		responseBody = string(body)
	}

	return &interfaces.HTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    headers,
		Body:       responseBody,
		Duration:   time.Since(startTime).Milliseconds(),
	}, nil
}

func prepareStreamRequest(streamingMode interfaces.StreamingMode, req *http.Request) {
	// Set stream type specific request headers.
	switch streamingMode {
	case interfaces.StreamingModeSSE:
		// Compatible processing of Accept header: the first one must be text/event-stream, use */* as a cover.
		acceptValue := req.Header.Get("Accept")
		acceptValues := strings.Split(acceptValue, ",")
		if len(acceptValues) == 0 || acceptValues[0] != "text/event-stream" {
			acceptValues = append([]string{"text/event-stream"}, acceptValues...)
		}
		acceptValues = append(acceptValues, "*/*")
		// Remove duplicates.
		acceptValues = utils.UniqueStrings(acceptValues)
		req.Header.Set("Accept", strings.Join(acceptValues, ", "))
		req.Header.Set("Cache-Control", "no-cache") // Disable caching.
		req.Header.Set("Connection", "keep-alive")  // stay connected.
	case interfaces.StreamingModeHTTP:
		req.Header.Set("Transfer-Encoding", "chunked") // Chunked transfer.
		req.Header.Set("Connection", "Upgrade")        // Upgrade connection.
	}
}

// Preprocessing response headers.
func preprocessResponseHeaders(streamingMode interfaces.StreamingMode, headerWriter http.ResponseWriter) {
	// Processed according to streaming mode.
	switch streamingMode {
	case interfaces.StreamingModeSSE:
		headerWriter.Header().Set("Content-Type", "text/event-stream")
		headerWriter.Header().Set("Cache-Control", privateStreamingCacheControl(headerWriter.Header().Get("Cache-Control")))
		headerWriter.Header().Set("Connection", "keep-alive")
		// Remove possible Content-Length header.
		headerWriter.Header().Del("Content-Length")
	case interfaces.StreamingModeHTTP:
		headerWriter.Header().Set("Transfer-Encoding", "chunked")
		headerWriter.Header().Set("X-Content-Type-Options", "nosniff")
		// Remove possible Content-Length header.
		headerWriter.Header().Del("Content-Length")
	}
}

func privateStreamingCacheControl(value string) string {
	directives := make([]string, 0)
	hasNoStore := false
	for _, directive := range strings.Split(value, ",") {
		directive = strings.TrimSpace(directive)
		if directive == "" || strings.EqualFold(directive, "public") || strings.EqualFold(directive, "private") {
			continue
		}
		if strings.EqualFold(directive, "no-store") {
			hasNoStore = true
		}
		directives = append(directives, directive)
	}

	directives = append(directives, "private")
	if !hasNoStore && !containsCacheDirective(directives, "no-cache") {
		directives = append(directives, "no-cache")
	}
	return strings.Join(directives, ", ")
}

func containsCacheDirective(directives []string, name string) bool {
	for _, directive := range directives {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(directive, "=", 2)[0]), name) {
			return true
		}
	}
	return false
}
