// Package proxy provides a simple HTTP proxy server that can be used to forward requests to a target server.
package proxy

import (
	"context"
	"fmt"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

var (
	syncOnce sync.Once
	s        *ProxyServer
)

// const (
// 	defaultCleanupInterval = 5 * time.Minute
// )

// ProxyServer proxy server.
type ProxyServer struct {
	Forwarder Forwarder // transponder.
	// Pool *clientPool // client pool.
}

// NewProxyServer creates a new proxy server.
func NewProxyServer() interfaces.ProxyHandler {
	syncOnce.Do(func() {
		s = &ProxyServer{
			Forwarder: NewForwarder(),
		}
	})
	return s
}

// HandlerRequest handles the request.
func (s *ProxyServer) HandlerRequest(ctx context.Context, req *interfaces.HTTPRequest) (resp *interfaces.HTTPResponse, err error) {
	// Get execution mode from context, request header first.
	executionMode := common.GetExecutionModeFromCtx(ctx)
	if executionMode != "" {
		req.ExecutionMode = executionMode
	}
	switch req.ExecutionMode {
	case interfaces.ExecutionModeSync:
		// Verify request parameters.
		resp, err = s.Forwarder.Forward(ctx, req)
	case interfaces.ExecutionModeStream:
		// Verify request parameters.
		resp, err = s.Forwarder.ForwardStream(ctx, req)
	case interfaces.ExecutionModeAsync:
		// Asynchronous mode is not supported at the moment.
		err = fmt.Errorf("async execution mode is not supported")
	default:
		// Verify request parameters.
		resp, err = s.Forwarder.Forward(ctx, req)
	}
	return resp, err
}
