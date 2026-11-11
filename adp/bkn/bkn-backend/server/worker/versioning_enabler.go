// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	versioningEnablerDelay    = 30 * time.Second
	versioningEnablerInterval = 10 * time.Minute
	// versioningEnablerNotes is the note of the first version of a network made before
	// every network was versioned.
	versioningEnablerNotes = "Versioning turned on by the server: every knowledge network is versioned"
)

// VersioningEnabler turns versioning on for every network that does not have it. Every
// network is versioned: after an upgrade it versions the networks made before, and later it
// catches a network whose versioning failed to turn on when it was created. Turning it on
// locks the network and refuses a network that has it already, so pods running this at the
// same time do each network once.
type VersioningEnabler struct {
	va       interfaces.KNVersionAccess
	kvs      func() interfaces.KNVersionService
	delay    time.Duration
	interval time.Duration
	stop     chan struct{}
}

// NewVersioningEnabler takes the version service as a function: it is built with the request
// handlers, after the workers.
func NewVersioningEnabler(va interfaces.KNVersionAccess, kvs func() interfaces.KNVersionService) *VersioningEnabler {
	return &VersioningEnabler{va: va, kvs: kvs, delay: versioningEnablerDelay,
		interval: versioningEnablerInterval, stop: make(chan struct{})}
}

// Start runs a round shortly after start and then at each interval, until Stop.
func (e *VersioningEnabler) Start() {
	if e == nil || e.va == nil {
		return
	}
	timer := time.NewTimer(e.delay)
	defer timer.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-timer.C:
			e.RunOnce(context.Background())
			timer.Reset(e.interval)
		}
	}
}

func (e *VersioningEnabler) Stop() {
	if e != nil {
		close(e.stop)
	}
}

// RunOnce versions each network that has no versioning yet, one at a time: turning it on
// copies the network twice and builds the concept index of its first version. A network that
// fails is logged and tried again next round.
func (e *VersioningEnabler) RunOnce(ctx context.Context) (enabled int) {
	kvs := e.kvs()
	if kvs == nil {
		return 0
	}
	ids, err := e.va.ListUnversionedKNIDs(ctx)
	if err != nil {
		otellog.LogError(ctx, "List the knowledge networks without versioning failed", err)
		return 0
	}
	for _, id := range ids {
		select {
		case <-e.stop:
			return enabled
		default:
		}
		ok, err := kvs.EnableUnversioned(ctx, id, versioningEnablerNotes)
		if err != nil {
			otellog.LogError(ctx, fmt.Sprintf("Turn on versioning of knowledge network %s failed", id), err)
			continue
		}
		if ok {
			enabled++
		}
	}
	if enabled > 0 {
		otellog.LogInfo(ctx, fmt.Sprintf("Turned on versioning of %d knowledge networks", enabled))
	}
	return enabled
}
