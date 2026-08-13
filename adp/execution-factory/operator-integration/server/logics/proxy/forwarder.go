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

// Forwarder HTTP请求转发器接口
type Forwarder interface {
	Forward(ctx context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error)
	ForwardStream(ctx context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error)
}

// forwarder HTTP请求转发器
type forwarder struct {
	pool            *clientPool
	streamProcessor *StreamProcessor
	logger          interfaces.Logger
}

var (
	forwarderOnce sync.Once
	f             Forwarder
)

// NewForwarder 创建一个新的HTTP请求转发器
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

// HTTPStreamForward 处理HTTP流式请求
func (f *forwarder) ForwardStream(ctx context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error) {
	startTime := time.Now()
	// 验证请求参数
	streamingMode, ok := common.GetStreamingModeFromCtx(ctx)
	if !ok {
		streamingMode = interfaces.StreamingModeHTTP
	}
	// 获取响应写入器
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
	// 创建不带超时的客户端用于流式请求
	streamClient := f.pool.GetStreamClient(streamingMode, req.Timeout)

	// 新添加：为流式请求设置必要的请求头
	prepareStreamRequest(streamingMode, httpReq)
	now := time.Now()
	f.logger.Debugf("do stream request, streamingMode: %v, timeout: %v", streamingMode, req.Timeout)
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		// 流式服务端，设置ResponseHeaderTimeout参数为10s, 10s内无请求头返回，认为是超时
		// 如果服务端不支持流式请求，但是客户端使用流式代理，也认为是超时
		// 超时错误示例: net/http: timeout awaiting response headers"
		headerWriter.WriteHeader(http.StatusRequestTimeout)
		if strings.Contains(err.Error(), "timeout awaiting response headers") {
			err = errors.Wrapf(err, "The server may not support streaming requests, or the server response timed out with no response headers received within 10 seconds")
			err = myErr.DefaultHTTPError(ctx, http.StatusRequestTimeout, err.Error())
		} else {
			// 请求转发失败，返回服务器不可用，请检查是否可用，或稍后重试
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
	// 复制响应头到原始响应
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
	// 确保设置正确的状态码
	headerWriter.WriteHeader(resp.StatusCode)
	// 添加这行代码以确保响应头被立即发送
	if flusher, ok := headerWriter.(http.Flusher); ok {
		flusher.Flush()
	}
	// 根据流式模式处理
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

// Forward 转发HTTP请求
func (f *forwarder) Forward(ctx context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error) {
	startTime := time.Now()

	// 获取HTTP客户端
	client := f.pool.GetClient(req.Timeout)

	// 构建HTTP请求
	httpReq, err := f.buildRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	// 发送请求
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

	// 处理响应
	return f.processResponse(resp, startTime)
}

// buildRequest 根据请求参数构建HTTP请求
func (f *forwarder) buildRequest(ctx context.Context, req *interfaces.HTTPRequest) (*http.Request, error) {
	// 处理URL和路径参数
	requestURL := substitutePathParams(req.URL, req.PathParams)
	// 路径模板仍有未替换的占位符时直接拒绝，避免把 "/market/{operator_id}" 这类无效 URL 发给下游
	if unresolved := unresolvedPathPlaceholders(requestURL); len(unresolved) > 0 {
		missing := strings.Join(unresolved, ", ")
		return nil, myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtProxyPathParamMissing,
			fmt.Sprintf("unresolved path placeholder(s) [%s] in url %s", missing, requestURL), missing)
	}
	// 处理查询参数
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

	// 处理请求体
	var reqBody io.Reader
	var contentType string
	// forceContentType 表示编码过程重算出的 Content-Type 必须覆盖调用方传入的同名请求头
	var forceContentType bool

	// GET/HEAD 语义上没有请求体，而调试面板对无 body 的接口固定发 "body": {}，
	// 直接按普通请求体处理会给下游带上空 JSON 体和 Content-Type，部分服务端会直接拒绝。
	payload := req.Body
	if isBodylessMethod(req.Method) && isEmptyBody(payload) {
		payload = nil
	}

	if payload != nil {
		// 检查Content-Type头
		contentType = ""
		if req.Headers != nil {
			for k, v := range req.Headers {
				if strings.EqualFold(k, "content-type") {
					contentType = fmt.Sprintf("%v", v)
					break
				}
			}
		}
		// 根据Content-Type处理请求体
		switch {
		case strings.Contains(contentType, "application/json"):
			// JSON格式
			jsonData, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			reqBody = bytes.NewBuffer(jsonData)
		case strings.Contains(contentType, "application/x-www-form-urlencoded"):
			// 表单格式
			formData := url.Values{}

			// 尝试将body转换为map
			if bodyMap, ok := payload.(map[string]interface{}); ok {
				for key, value := range bodyMap {
					formData.Add(key, fmt.Sprintf("%v", value))
				}
			}
			reqBody = strings.NewReader(formData.Encode())
		case strings.Contains(contentType, "multipart/form-data"):
			// 多部分表单格式
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)

			// 尝试将body转换为map
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
			// writer 生成的 Content-Type 带 boundary，必须覆盖调用方自带的 multipart 头，否则下游无法解析
			contentType = writer.FormDataContentType()
			forceContentType = true
			reqBody = body
		case strings.Contains(contentType, "text/plain"):
			// 文本格式
			reqBody = strings.NewReader(fmt.Sprintf("%v", payload))
		case strings.Contains(contentType, "text/event-stream"):
			// SSE格式
			reqBody = strings.NewReader(fmt.Sprintf("%v", payload))
		case strings.Contains(contentType, "application/stream+json"), // HTTP Streaming格式
			strings.Contains(contentType, "application/x-ndjson"),      // NDJSON格式
			strings.Contains(contentType, "application/x-json-stream"): // HTTP Streaming格式
			// HTTP Streaming格式
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

	// 创建HTTP请求
	httpReq, err := http.NewRequest(req.Method, requestURL, reqBody)
	if err != nil {
		err = fmt.Errorf("failed to create request: %v", err)
		return nil, err
	}

	// 设置请求头，并用当前请求上下文补齐 trace/request id，保证代理链路可聚合。
	// 公开接口的 header 由调用方直接填写，身份头必须回填成本次请求的认证账户。
	isPublic := common.IsPublicAPIFromCtx(ctx)
	auth, _ := common.GetAccountAuthContextFromCtx(ctx)
	headers := map[string]string{}
	for key, value := range req.Headers {
		// 传输层请求头由转发器与 Go HTTP 客户端接管，调用方手填只会让实际请求与预览对不上
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

	// 如果Content-Type未在请求头中设置，但我们有确定的类型，则设置它
	if contentType != "" && (forceContentType || httpReq.Header.Get("Content-Type") == "") {
		httpReq.Header.Set("Content-Type", contentType)
	}

	return httpReq, nil
}

// transportHeaders 由转发链路自身接管的传输层请求头，调用方传入时一律丢弃。
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

// isTransportHeader 判断请求头是否属于传输层请求头（大小写不敏感）。
func isTransportHeader(key string) bool {
	_, ok := transportHeaders[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

// identityHeaders 身份请求头：内置工具箱把它们声明成普通 OpenAPI header 参数，
// 下游 /in 接口不验 token、直接据此判定调用者身份并做 per-account 授权。
// 公开接口（调试与执行）的 header 完全由调用方填写，放任透传等于允许冒充任意账户，
// 因此这里统一回填成本次请求认证出来的账户。内部接口维持透传，运行时按 /in 约定注入身份。
var identityHeaders = map[string]struct{}{
	"x-account-id":   {},
	"x-account-type": {},
	"user_id":        {},
	"x-user-id":      {},
}

// isIdentityHeader 判断请求头是否属于身份请求头（大小写不敏感）。
func isIdentityHeader(key string) bool {
	_, ok := identityHeaders[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

// resolveIdentityHeader 返回身份请求头应有的值；返回 false 表示拿不到认证账户，该请求头必须丢弃。
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

// bodylessMethods 语义上不带请求体的方法。
var bodylessMethods = map[string]struct{}{
	http.MethodGet:  {},
	http.MethodHead: {},
}

// isBodylessMethod 判断方法是否语义上不带请求体（大小写不敏感，空方法按 GET 处理）。
func isBodylessMethod(method string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(method))
	if normalized == "" {
		normalized = http.MethodGet
	}
	_, ok := bodylessMethods[normalized]
	return ok
}

// isEmptyBody 判断请求体是否为空信封：调试面板对无 body 的接口固定发 "body": {}，
// 只有空值才当作没有请求体，非空 body 仍按调用方声明的方式编码发出。
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

// pathPlaceholderPattern 匹配路径里形如 {name} 的占位符。
var pathPlaceholderPattern = regexp.MustCompile(`\{([^{}/]+)\}`)

// colonParamPattern 匹配 ":name" 形式的占位符，并要求后面是非标识符字符或结尾。
// 少了这个边界，参数 id 会把 "/users/:identifier" 里的 ":id" 前缀也换掉，剩下半截 "entifier"。
func colonParamPattern(key string) *regexp.Regexp {
	return regexp.MustCompile(`:` + regexp.QuoteMeta(key) + `([^A-Za-z0-9_]|$)`)
}

// substitutePathParams 把 path 参数按 "{name}"、":{name}"、":name" 三种写法替换进 URL。
//
// 值一律按路径段转义：path 参数在 OpenAPI 里是单个路径段，未转义的 "/"、"?"、"#"
// 会改变整条 URL 的结构——调用方本想传 ID，却能把下游请求改写成另一个路径、或凭空
// 追加 query。目标 URL 由工具元数据决定、调试界面不允许改，这里是最后一道闸。
func substitutePathParams(rawURL string, params map[string]string) string {
	if len(params) == 0 {
		return rawURL
	}

	for key, value := range params {
		escaped := url.PathEscape(value)
		// ":{key}" 必须先于 "{key}" 替换，否则会先命中 "{key}" 而在 URL 里留下多余的冒号
		rawURL = strings.ReplaceAll(rawURL, ":{"+key+"}", escaped)
		rawURL = strings.ReplaceAll(rawURL, "{"+key+"}", escaped)
		// 用 Func 版本替换：转义后的值可能含 "$"，走 ReplaceAllString 会被当成分组引用。
		rawURL = colonParamPattern(key).ReplaceAllStringFunc(rawURL, func(match string) string {
			return escaped + match[len(key)+1:]
		})
	}

	return rawURL
}

// unresolvedPathPlaceholders 返回 URL 路径中仍未被 path 参数替换的占位符名称。
// 只检查路径部分：query 与 fragment 里的花括号可能是业务值，冒号形式无法与端口号区分，都不参与判断。
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

// processResponse 处理HTTP响应
func (f *forwarder) processResponse(resp *http.Response, startTime time.Time) (*interfaces.HTTPResponse, error) {
	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// 解析响应头
	headers := make(map[string]any)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	// 尝试解析JSON响应
	var responseBody interface{}
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		if err := json.Unmarshal(body, &responseBody); err != nil {
			// 如果解析失败，使用原始响应体
			responseBody = string(body)
		}
	} else {
		// 非JSON响应，使用字符串
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
	// 设置流类型特定请求头
	switch streamingMode {
	case interfaces.StreamingModeSSE:
		// 兼容处理Accept头: 第一个必须为text/event-stream，使用*/*作为兜底
		acceptValue := req.Header.Get("Accept")
		acceptValues := strings.Split(acceptValue, ",")
		if len(acceptValues) == 0 || acceptValues[0] != "text/event-stream" {
			acceptValues = append([]string{"text/event-stream"}, acceptValues...)
		}
		acceptValues = append(acceptValues, "*/*")
		// 去重
		acceptValues = utils.UniqueStrings(acceptValues)
		req.Header.Set("Accept", strings.Join(acceptValues, ", "))
		req.Header.Set("Cache-Control", "no-cache") // 禁用缓存
		req.Header.Set("Connection", "keep-alive")  // 保持连接
	case interfaces.StreamingModeHTTP:
		req.Header.Set("Transfer-Encoding", "chunked") // 分块传输
		req.Header.Set("Connection", "Upgrade")        // 升级连接
	}
}

// 预处理响应头
func preprocessResponseHeaders(streamingMode interfaces.StreamingMode, headerWriter http.ResponseWriter) {
	// 根据流式模式处理
	switch streamingMode {
	case interfaces.StreamingModeSSE:
		headerWriter.Header().Set("Content-Type", "text/event-stream")
		headerWriter.Header().Set("Cache-Control", privateStreamingCacheControl(headerWriter.Header().Get("Cache-Control")))
		headerWriter.Header().Set("Connection", "keep-alive")
		// 移除可能存在的Content-Length头部
		headerWriter.Header().Del("Content-Length")
	case interfaces.StreamingModeHTTP:
		headerWriter.Header().Set("Transfer-Encoding", "chunked")
		headerWriter.Header().Set("X-Content-Type-Options", "nosniff")
		// 移除可能存在的Content-Length头部
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
