// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
)

func TestBKNTraceRequestContextReadsBusinessCausalityHeaders(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	for key, value := range map[string]string{
		"bkn-request-id": "req_backend_headers_001",
		"x-account-id":   "acct_demo", "x-account-type": "service",
		"bkn-interaction-id": "int_backend_001", "bkn-operation-id": "op_backend_001",
		"bkn-causation-event-id": "evt_upstream_001", "bkn-claim-id": "claim_upstream_001",
	} {
		c.Request.Header.Set(key, value)
	}

	got := bknTraceRequestContext(c, hydra.Visitor{})
	if got.InteractionID != "int_backend_001" || got.OperationID != "op_backend_001" || got.CausationEventID != "evt_upstream_001" || got.ClaimID != "claim_upstream_001" || got.Attempt != 1 {
		t.Fatalf("causality headers not parsed: %#v", got)
	}
}

func TestBKNTraceRequestContextCreatesReplayEnvelopeForDirectStudioRequest(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set("x-account-id", "acct_demo")
	c.Request.Header.Set("x-account-type", "user")

	got := bknTraceRequestContext(c, hydra.Visitor{})
	if got.RequestID == "" || !strings.HasPrefix(got.InteractionID, "int_") || !strings.HasPrefix(got.OperationID, "op_") {
		t.Fatalf("direct request replay envelope not generated: %#v", got)
	}
	if _, err := time.Parse(time.RFC3339Nano, got.ObservedAt); err != nil {
		t.Fatalf("observed_at is not RFC3339Nano: %q: %v", got.ObservedAt, err)
	}
	for header, want := range map[string]string{
		headerBKNRequestID:       got.RequestID,
		headerBKNInteractionID:   got.InteractionID,
		headerBKNOperationID:     got.OperationID,
		headerBKNEventObservedAt: got.ObservedAt,
	} {
		if gotHeader := recorder.Header().Get(header); gotHeader != want {
			t.Fatalf("response header %s=%q, want %q", header, gotHeader, want)
		}
	}
}
