// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
package captureadmission

import (
	"sync"
	"time"
)

// View is the process-local admission decision. It is refreshed by the
// existing capture-policy controller; business write paths only read it and
// never perform a policy RPC or database query.
type View struct {
	mu       sync.RWMutex
	revision uint64
	state    string
	known    bool
	updated  time.Time
}

func New(revision uint64, state string) *View {
	if state == "" {
		state = "enabled"
	}
	return &View{revision: revision, state: state, known: revision > 0 && (state == "enabled" || state == "disabled"), updated: time.Now()}
}

func (v *View) Update(revision uint64, state string) {
	if v == nil || state == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if state != "enabled" && state != "disabled" {
		return
	}
	if revision >= v.revision {
		v.revision, v.state = revision, state
		v.known = true
		v.updated = time.Now()
	}
}

func (v *View) Snapshot() (uint64, string) {
	if v == nil {
		return 0, "enabled"
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.revision, v.state
}

func (v *View) AllowsNewRecords() bool {
	if !v.Known() {
		return false
	}
	_, state := v.Snapshot()
	return state == "enabled"
}

func (v *View) Known() bool {
	if v == nil {
		return true
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.known && time.Since(v.updated) <= 30*time.Second
}

func (v *View) Disabled() bool {
	revision, state := v.Snapshot()
	return revision > 0 && state == "disabled"
}
