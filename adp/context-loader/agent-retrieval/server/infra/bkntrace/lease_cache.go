// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"net/http"
	"time"
)

const maxCachedInteractionLeases = 256
const interactionLeaseCacheTTL = time.Minute

type interactionLeaseKey struct {
	owner          [6]string
	conversationID string
	interactionID  string
}

type cachedInteractionLease struct {
	interaction Interaction
	expiresAt   time.Time
}

func leaseKey(ctx context.Context, conversationID, interactionID string) (interactionLeaseKey, bool) {
	headers := make(http.Header)
	if setTrustedLifecycleHeaders(ctx, headers) != nil {
		return interactionLeaseKey{}, false
	}
	return interactionLeaseKey{owner: [6]string{
		headers.Get("x-account-id"), headers.Get("x-account-type"),
		headers.Get("X-BKN-Application-Principal-ID"), headers.Get("X-BKN-Effective-Subject-Type"),
		headers.Get("X-BKN-Effective-Subject-ID"), headers.Get("X-BKN-Auth-Method"),
	}, conversationID: conversationID, interactionID: interactionID}, true
}

func (c *LifecycleClient) cachedLease(ctx context.Context, conversationID, interactionID string) (Interaction, bool) {
	key, ok := leaseKey(ctx, conversationID, interactionID)
	if !ok {
		return Interaction{}, false
	}
	c.leaseMu.Lock()
	defer c.leaseMu.Unlock()
	entry, ok := c.leases[key]
	if !ok || !entry.expiresAt.After(time.Now()) {
		delete(c.leases, key)
		return Interaction{}, false
	}
	return entry.interaction, true
}

func (c *LifecycleClient) rememberLease(ctx context.Context, interaction Interaction) {
	key, ok := leaseKey(ctx, interaction.ConversationID, interaction.InteractionID)
	if !ok || interaction.InteractionID == "" || interaction.ConversationID == "" {
		return
	}
	c.leaseMu.Lock()
	defer c.leaseMu.Unlock()
	now := time.Now()
	if interaction.ExecutionStatus != "active" || interaction.LeaseToken == "" || interaction.LeaseEpoch == 0 || !interaction.LeaseExpiresAt.After(now) {
		delete(c.leases, key)
		return
	}
	if c.leases == nil {
		c.leases = make(map[interactionLeaseKey]cachedInteractionLease)
	}
	for existing, entry := range c.leases {
		if !entry.expiresAt.After(now) {
			delete(c.leases, existing)
		}
	}
	if _, exists := c.leases[key]; !exists && len(c.leases) >= maxCachedInteractionLeases {
		// Eviction only costs a fresh authorized GET; no lifecycle state is cached.
		for existing := range c.leases {
			delete(c.leases, existing)
			break
		}
	}
	expiresAt := now.Add(interactionLeaseCacheTTL)
	if interaction.LeaseExpiresAt.Before(expiresAt) {
		expiresAt = interaction.LeaseExpiresAt
	}
	// Retain credentials only. Core still checks ownership, status and fencing on
	// every ensure; a cached interaction never grants local execution permission.
	c.leases[key] = cachedInteractionLease{interaction: Interaction{
		ConversationID: interaction.ConversationID, InteractionID: interaction.InteractionID,
		ExecutionStatus: "active", LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}, expiresAt: expiresAt}
}

func (c *LifecycleClient) forgetLease(ctx context.Context, conversationID, interactionID string) {
	key, ok := leaseKey(ctx, conversationID, interactionID)
	if !ok {
		return
	}
	c.leaseMu.Lock()
	delete(c.leases, key)
	c.leaseMu.Unlock()
}
