// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

var errInteractionFinishACK = errors.New("interaction finish acknowledgement does not match the requested terminal transition")

func interactionFinishTimeout() time.Duration {
	ms, err := strconv.Atoi(os.Getenv("BKN_TRACE_INTERACTION_FINISH_TIMEOUT_MS"))
	if err != nil || ms <= 0 || ms > 30000 {
		return 5 * time.Second
	}
	return time.Duration(ms) * time.Millisecond
}

func retryableInteractionFinish(apiErr *bkntrace.APIError, err error) bool {
	if errors.Is(err, bkntrace.ErrFeatureNotInstalled) || apiErr != nil && apiErr.Code == "feature_not_installed" {
		return false
	}
	var coreErr *bkntrace.CoreHTTPError
	if errors.As(err, &coreErr) && coreErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return errors.Is(err, errInteractionFinishACK) || bkntrace.IsTraceInfrastructureFailure(apiErr, err) || apiErr != nil && apiErr.HTTPStatus == http.StatusTooManyRequests
}

// A requested terminal transition is distinct from best-effort observation.
// Once an explicit finish request has passed preparation, retain its bounded commit attempt
// across caller disconnects, just as the operation Guard does. Resubmit only
// the identical body/key; Core alone decides whether that finish matches.
func callInteractionFinish(ctx context.Context, client *bkntrace.LifecycleClient, path, interactionID string, body map[string]any, target *bkntrace.Interaction) (*bkntrace.APIError, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), interactionFinishTimeout())
	defer cancel()
	var apiErr *bkntrace.APIError
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		*target = bkntrace.Interaction{}
		apiErr, err = client.Call(ctx, http.MethodPost, path, json.RawMessage(raw), target)
		if errors.Is(err, bkntrace.ErrInvalidLifecycleResponse) {
			err = errInteractionFinishACK
		}
		if apiErr == nil && err == nil && (target.InteractionID != interactionID || target.ExecutionStatus != stringValue(body["outcome"])) {
			err = errInteractionFinishACK
		}
		if !retryableInteractionFinish(apiErr, err) {
			return apiErr, err
		}
		if attempt < 2 {
			timer := time.NewTimer(20 * time.Millisecond * time.Duration(1<<attempt))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return apiErr, err
}

func interactionFinishUnconfirmed(ctx context.Context, stage string, cause lifecycleError) *mcpsdk.CallToolResult {
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	return lifecycleToolErrorWithDetails(lifecycleError{
		Code:      "interaction_finish_unconfirmed",
		Message:   "Interaction finish is not confirmed. Retry the same finish with the same answer; do not repeat business tools.",
		Retryable: true, RequiredAction: "retry_same_finish", RequestID: traceContext.RequestID,
	}, map[string]any{"stage": stage, "cause_code": cause.Code, "finish_confirmed": false, "trace_recorded": false, "trace_available": false})
}
