// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package main

import (
	"context"
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
