// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package content_task holds the socket through which an edition build installs a content worker client.
package content_task

import (
	"context"
	"errors"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// ErrContentTaskDispatcherFrozen is returned when a dispatcher is installed after the service started running.
var ErrContentTaskDispatcherFrozen = errors.New("content task dispatcher is frozen")

// ErrNilContentTaskDispatcher is returned when installing a nil dispatcher.
var ErrNilContentTaskDispatcher = errors.New("content task dispatcher must not be nil")

var (
	mu         sync.RWMutex
	dispatcher interfaces.ContentTaskDispatcher = noopDispatcher{}
	frozen     bool
)

// SetContentTaskDispatcher installs d during application assembly, between Boot and Run.
func SetContentTaskDispatcher(d interfaces.ContentTaskDispatcher) error {
	if d == nil {
		return ErrNilContentTaskDispatcher
	}
	mu.Lock()
	defer mu.Unlock()
	if frozen {
		return ErrContentTaskDispatcherFrozen
	}
	dispatcher = d
	return nil
}

// FreezeContentTaskDispatcher stops further installs; Run calls it before serving.
func FreezeContentTaskDispatcher() {
	mu.Lock()
	defer mu.Unlock()
	frozen = true
}

// GetContentTaskDispatcher returns the installed dispatcher, or the no-op one when none was installed.
func GetContentTaskDispatcher() interfaces.ContentTaskDispatcher {
	mu.RLock()
	defer mu.RUnlock()
	return dispatcher
}

// noopDispatcher is the core default: no content worker is available.
type noopDispatcher struct{}

func (noopDispatcher) Enabled(context.Context) bool { return false }

func (noopDispatcher) Dispatch(context.Context, *interfaces.ContentTaskRequest) (*interfaces.ContentTaskRef, error) {
	return nil, interfaces.NewContentTaskUnavailableError(nil)
}

func (noopDispatcher) GetStatus(context.Context, string) (*interfaces.ContentTaskStatus, error) {
	return nil, interfaces.NewContentTaskUnavailableError(nil)
}
