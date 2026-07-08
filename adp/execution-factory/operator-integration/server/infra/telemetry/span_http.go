package telemetry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var clearUserPassRe = regexp.MustCompile(`(://)[^/]*@`)

// HTTPRequest 发起HTTP请求
func HTTPRequest(ctx context.Context, req *http.Request, fn func(req *http.Request) (*http.Response, error)) (rsp *http.Response, err error) {
	tracer := otel.GetTracerProvider()
	if tracer != nil {
		var span trace.Span
		ctx, span = oteltrace.StartNamedClientSpan(ctx, "http.request")
		arr := strings.Split(req.Proto, "/")
		span.SetAttributes(attribute.Key("net.protocol.name").String(arr[0]))
		if len(arr) > 1 {
			span.SetAttributes(attribute.Key("net.protocol.version").String(arr[1]))
		}
		span.SetAttributes(attribute.Key("http.method").String(req.Method))
		span.SetAttributes(attribute.Key("http.request_content_length").Int64(req.ContentLength))
		span.SetAttributes(attribute.Key("http.url").String(clearUserPassRe.ReplaceAllString(req.URL.String(), "$1")))
		span.SetAttributes(attribute.Key("net.peer.name").String(req.URL.Hostname()))
		span.SetAttributes(attribute.Key("net.peer.port").String(req.URL.Port()))
		if req.Header == nil {
			req.Header = http.Header{}
		}
		otel.GetTextMapPropagator().Inject(trace.ContextWithSpan(ctx, span), propagation.HeaderCarrier(req.Header))
		req = req.WithContext(ctx)
		defer func() {
			if rsp != nil {
				span.SetAttributes(attribute.Key("http.status_code").Int(rsp.StatusCode))
				span.SetAttributes(attribute.Key("http.response_content_length").Int64(rsp.ContentLength))
			}
			// 400以上的错误记录到trace中
			e := err
			if e == nil {
				e = recordHTTPErrorBody(rsp)
			}
			oteltrace.EndSpan(ctx, e)
		}()
	}
	rsp, err = fn(req)
	return
}

func recordHTTPErrorBody(rsp *http.Response) (err error) {
	// 只记录 400以上错误
	if rsp == nil || rsp.Body == nil {
		return nil
	}
	if rsp.StatusCode < http.StatusBadRequest {
		return
	}
	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		return
	}
	rsp.Body = io.NopCloser(bytes.NewBuffer(body)) // 将body重新赋值给rsp.Body，后续可以读取
	limitBody := body
	err = errors.New(string(limitBody))
	return
}

// BuildUpOperateName 获取TraceOperate名称
func BuildUpOperateName(ops ...string) string {
	return strings.Join(ops, ".")
}
