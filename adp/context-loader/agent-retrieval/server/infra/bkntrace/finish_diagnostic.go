// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bkntrace

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"syscall"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

// A terminal receipt can remain pending after a write or either recovery read.
// Keep the actual failing boundary without logging request/response bodies,
// authorization headers, upstream messages or URLs.
func logLifecycleIOFailure(ctx context.Context, state GuardState, phase string, attempt int, apiErr *APIError, err error) {
	if err == nil && !IsTraceInfrastructureFailure(apiErr, nil) {
		return
	}
	correlation, _ := common.GetTraceContextFromCtx(ctx)
	kind, code, status := "transport_error", "", 0
	if apiErr != nil {
		kind, status = "core_error", apiErr.HTTPStatus
		code = safeFinishDiagnosticCode(apiErr.Code)
	}
	var networkErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		kind = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		kind = "canceled"
	case errors.Is(err, syscall.ECONNREFUSED):
		kind = "connection_refused"
	case errors.Is(err, syscall.ECONNRESET):
		kind = "connection_reset"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		kind = "unexpected_eof"
	case errors.As(err, &networkErr) && networkErr.Timeout():
		kind = "timeout"
	}
	log.Printf("BKN Trace lifecycle I/O failed operation_id=%q request_id=%q operation_attempt=%d recovery_attempt=%d phase=%q failure_kind=%q http_status=%d error_code=%q",
		state.Result.Operation.OperationID, correlation.RequestID, state.Result.Operation.Attempt, attempt+1, phase, kind, status, code)
}

func safeFinishDiagnosticCode(code string) string {
	switch code {
	case "internal_error", "trace_core_unavailable", "authorization_unavailable", "service_unavailable", "receipt_pending", "feature_not_installed", "evidence_capture_unavailable":
		return code
	default:
		return "other"
	}
}
