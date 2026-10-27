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
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

// A lost start ACK must replay the exact Core request, not create another
// interaction or tell the caller to start a new one. Recovery shares the existing
// pre budget, preserves owner/key/input, and never retries domain refusals.
func callInteractionStart(ctx context.Context, client *bkntrace.LifecycleClient, path, conversationID string, body map[string]any, target *bkntrace.Interaction) (*bkntrace.APIError, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var apiErr *bkntrace.APIError
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		attemptContext, cancel := bkntrace.LifecycleAttemptContext(ctx)
		*target = bkntrace.Interaction{}
		apiErr, err = client.Call(attemptContext, http.MethodPost, path, json.RawMessage(raw), target)
		cancel()
		if apiErr == nil && err == nil && (target.InteractionID == "" || target.ConversationID != strings.TrimSpace(conversationID)) {
			err = bkntrace.ErrInvalidLifecycleResponse
		}
		if apiErr == nil && err == nil {
			return nil, nil
		}
		if errors.Is(err, bkntrace.ErrFeatureNotInstalled) || apiErr != nil && apiErr.Code == "feature_not_installed" {
			return apiErr, err
		}
		if !errors.Is(err, bkntrace.ErrInvalidLifecycleResponse) && !bkntrace.IsTraceInfrastructureFailure(apiErr, err) {
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
