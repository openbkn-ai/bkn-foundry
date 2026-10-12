// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestArtifactWritesReuseSuccessfulHTTPConnection(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"artifact_id":"art-1","created":true}`)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	previous := artifactHTTPClient
	artifactHTTPClient = server.Client()
	defer func() { artifactHTTPClient = previous }()
	for i := 0; i < 2; i++ {
		if err := postArtifactWithRetry(context.Background(), server.URL, time.Second, nil, map[string]any{"content": "answer"}); err != nil {
			t.Fatal(err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("two successful artifact writes opened %d connections, want 1", connections.Load())
	}
}

func TestArtifactAcknowledgementReadFailureDoesNotRetryCommittedWrite(t *testing.T) {
	previous := artifactHTTPClient
	defer func() { artifactHTTPClient = previous }()
	calls := 0
	artifactHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusCreated, Body: failedArtifactAcknowledgement{}, Header: make(http.Header)}, nil
	})}
	if err := postArtifactWithRetry(context.Background(), "http://artifact.test", time.Second, nil, map[string]any{}); err != nil {
		t.Fatalf("committed write became a failure: %v", err)
	}
	if calls != 1 {
		t.Fatalf("committed write retried %d times", calls)
	}
}

type failedArtifactAcknowledgement struct{}

func (failedArtifactAcknowledgement) Read([]byte) (int, error) {
	return 0, fmt.Errorf("acknowledgement connection lost")
}
func (failedArtifactAcknowledgement) Close() error { return nil }
