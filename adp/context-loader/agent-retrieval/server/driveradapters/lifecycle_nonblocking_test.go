// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package driveradapters

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

func TestHTTPBusinessContinuesWhenTraceIsUninstalled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	calls := 0
	router := gin.New()
	router.Use(trustedLifecycleHTTPContext(), middlewareLifecycle(bkntrace.NewLifecycleClient("", nil)))
	router.POST("/api/agent-retrieval/v1/kn/query_object_instance", func(c *gin.Context) {
		calls++
		c.JSON(http.StatusOK, gin.H{"business": "actual"})
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent-retrieval/v1/kn/query_object_instance", bytes.NewBufferString(`{"kn_id":"kn-1","ot_id":"object-1","bkn_context":{"conversation_id":"conv-1","interaction_id":"int-1"}}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if calls != 1 || response.Code != http.StatusOK || response.Body.String() != `{"business":"actual"}` {
		t.Fatalf("Trace prevented HTTP business: calls=%d status=%d body=%s", calls, response.Code, response.Body)
	}
	if response.Header().Get("X-OpenBKN-Trace-Recorded") != "false" {
		t.Fatalf("HTTP failed to disclose capture gap: %v", response.Header())
	}
}

func TestSpecHTTPNoBodyContextClearsUnconfirmedIDsAndDisclosesGap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(trustedLifecycleHTTPContext())
	r.Use(func(c *gin.Context) {
		ctx := common.SetTraceContextToCtx(c.Request.Context(), common.TraceContext{RequestID: "req_spec_unmanaged", ConversationID: "unconfirmed-conv", InteractionID: "unconfirmed-int", OperationID: "unconfirmed-op", ParentOperationID: "unconfirmed-parent", Attempt: 1})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(middlewareLifecycle(bkntrace.NewLifecycleClient("", nil)))
	calls := 0
	r.POST("/kn/query_object_instance", func(c *gin.Context) {
		calls++
		trace, _ := common.GetTraceContextFromCtx(c.Request.Context())
		if trace.InteractionID != "" || trace.OperationID != "" || trace.ParentOperationID != "" {
			t.Errorf("unconfirmed managed identity reaches business: int_present=%t op_present=%t parent_present=%t", trace.InteractionID != "", trace.OperationID != "", trace.ParentOperationID != "")
		}
		c.JSON(200, gin.H{"business": "actual"})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/kn/query_object_instance", bytes.NewBufferString(`{"kn_id":"kn-1","ot_id":"object-1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if calls != 1 || rec.Code != 200 {
		t.Fatal("business changed")
	}
	if rec.Header().Get("X-OpenBKN-Trace-Recorded") != "false" {
		t.Error("no-context HTTP business omits unrecorded metadata")
	}
}

func TestCancelledHTTPCallerDoesNotStartUnmanagedBusiness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	calls := 0
	router := gin.New()
	router.Use(middlewareLifecycle(bkntrace.NewLifecycleClient("", nil)))
	router.POST("/kn/query_object_instance", func(c *gin.Context) { calls++; c.Status(200) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/kn/query_object_instance", bytes.NewBufferString(`{"kn_id":"kn-1"}`)).WithContext(ctx)
	router.ServeHTTP(httptest.NewRecorder(), req)
	if calls != 0 {
		t.Fatalf("cancelled caller started %d new business executions", calls)
	}
}

type cancellationRoundTripFunc func(*http.Request) (*http.Response, error)

func (f cancellationRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestHTTPCancellationAfterSuccessfulEnsureDoesNotStartBusiness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls, ensures, finishes := 0, 0, 0
	client := bkntrace.NewLifecycleClient("http://trace.test", &http.Client{Transport: cancellationRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var value any
		if strings.HasSuffix(r.URL.Path, "/interactions/int-1") {
			value = bkntrace.Interaction{InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active", LeaseToken: "lease", LeaseEpoch: 1}
		} else if strings.HasSuffix(r.URL.Path, "/operations:ensure") {
			ensures++
			cancel()
			value = bkntrace.OperationResult{Execute: true, Operation: bkntrace.Operation{OperationID: "op-actual", Attempt: 1}, Receipt: bkntrace.Receipt{ReceiptID: "receipt-actual", ReceiptStatus: "pending"}}
		} else {
			finishes++
			value = map[string]any{}
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})})
	router := gin.New()
	router.Use(trustedLifecycleHTTPContext(), middlewareLifecycle(client))
	router.POST("/kn/query_object_instance", func(c *gin.Context) { calls++; c.Status(200) })
	req := httptest.NewRequest(http.MethodPost, "/kn/query_object_instance", bytes.NewBufferString(`{"kn_id":"kn-1","ot_id":"ot-1","bkn_context":{"conversation_id":"conv-1","interaction_id":"int-1"}}`)).WithContext(ctx)
	router.ServeHTTP(httptest.NewRecorder(), req)
	if ensures != 1 || calls != 0 || finishes != 0 {
		t.Fatalf("cancelled HTTP caller executed after registration: ensure=%d business=%d finish=%d", ensures, calls, finishes)
	}
}

func TestFencedLeaseRefreshFailureNeverStartsUnmanagedHTTPBusiness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, failure := range []string{"network", "http_503", "retry_network", "retry_503"} {
		t.Run(failure, func(t *testing.T) {
			gets, posts, calls := 0, 0, 0
			client := bkntrace.NewLifecycleClient("http://trace.test", &http.Client{Transport: cancellationRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				status := 200
				var value any
				if r.Method == http.MethodGet {
					gets++
					if gets > 1 && failure == "network" {
						return nil, syscall.ECONNREFUSED
					}
					value = bkntrace.Interaction{ConversationID: "conv-1", InteractionID: "int-1", ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 1, LeaseExpiresAt: time.Now().Add(time.Minute)}
					if gets > 1 && failure == "http_503" {
						status = 503
						value = map[string]any{"error": bkntrace.APIError{Code: "trace_core_unavailable"}}
					}
				} else {
					posts++
					if posts > 1 && failure == "retry_network" {
						return nil, syscall.ECONNREFUSED
					}
					status = 409
					value = map[string]any{"error": bkntrace.APIError{Code: "terminal_conflict", Message: "stale interaction lease was fenced"}}
					if posts > 1 && failure == "retry_503" {
						status = 503
						value = map[string]any{"error": bkntrace.APIError{Code: "trace_core_unavailable"}}
					}
				}
				raw, _ := json.Marshal(value)
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})})
			router := gin.New()
			router.Use(trustedLifecycleHTTPContext())
			router.Use(func(c *gin.Context) {
				var interaction bkntrace.Interaction
				apiErr, err := client.Call(c.Request.Context(), http.MethodGet, "/interactions/int-1", nil, &interaction)
				if apiErr != nil || err != nil {
					t.Fatalf("seed lease: %v %v", apiErr, err)
				}
				c.Next()
			}, middlewareLifecycle(client))
			router.POST("/kn/query_object_instance", func(c *gin.Context) { calls++; c.Status(200) })
			request := httptest.NewRequest(http.MethodPost, "/kn/query_object_instance", bytes.NewBufferString(`{"kn_id":"kn-1","ot_id":"ot-1","bkn_context":{"conversation_id":"conv-1","interaction_id":"int-1"}}`))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if calls != 0 || response.Code != 409 || !strings.Contains(response.Body.String(), "terminal_conflict") {
				t.Fatalf("fencing bypassed: calls=%d status=%d body=%s", calls, response.Code, response.Body.String())
			}
		})
	}
}
