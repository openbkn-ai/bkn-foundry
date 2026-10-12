// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package main

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestRunWaitsForShutdownToComplete(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	app := &testApplication{
		started:      make(chan struct{}),
		stopServer:   make(chan struct{}),
		shutdownDone: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() {
		result <- run(ctx, app)
	}()
	<-app.started
	cancel()

	select {
	case <-result:
		t.Fatal("run returned before shutdown completed")
	default:
	}
	close(app.shutdownDone)
	if err := <-result; err != nil {
		t.Fatalf("run application: %v", err)
	}
	if !app.shutdownCalled {
		t.Fatal("application shutdown was not called")
	}
}

type testApplication struct {
	started        chan struct{}
	stopServer     chan struct{}
	shutdownDone   chan struct{}
	startOnce      sync.Once
	shutdownCalled bool
}

func (a *testApplication) Start() error {
	a.startOnce.Do(func() { close(a.started) })
	<-a.stopServer
	return nil
}

func (a *testApplication) Shutdown(context.Context) error {
	a.shutdownCalled = true
	<-a.shutdownDone
	close(a.stopServer)
	return nil
}

func TestConfigureLogLevel(t *testing.T) {
	originalLevel := slog.SetLogLoggerLevel(slog.LevelInfo)
	defer slog.SetLogLoggerLevel(originalLevel)
	originalOutput := log.Writer()
	defer log.SetOutput(originalOutput)
	for _, tc := range []struct {
		value string
		debug bool
	}{
		{"", false},
		{"info", false},
		{" DEBUG ", true},
		{"warn", false},
		{"error", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			var output bytes.Buffer
			log.SetOutput(&output)
			if err := configureLogLevel(tc.value); err != nil {
				t.Fatal(err)
			}
			slog.Debug("dependency diagnostic", "trace_id", "budget-request")
			if got := strings.Contains(output.String(), "trace_id=budget-request"); got != tc.debug {
				t.Fatalf("debug output=%q, want emitted=%v", output.String(), tc.debug)
			}
		})
	}
	if err := configureLogLevel("invalid"); err == nil || !strings.Contains(err.Error(), "BKN_TRACE_LOG_LEVEL") {
		t.Fatalf("expected actionable configuration error, got %v", err)
	}
}
