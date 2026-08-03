// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

// Managed lifecycle state is caller-owned by design: a client that names its own
// conversation gets evidence bound to its real business turn. MCP clients such as
// Claude Code and Cursor have no business conversation to name, so without a
// fallback every tool call fails closed on conversation_required.
//
// The fallback binds one conversation to one MCP connection. Every tool call on
// that connection becomes an operation under a single interaction, because Core
// allows only one active interaction per conversation and MCP clients issue tool
// calls concurrently. The tradeoff is that the interaction never reaches a
// closure manifest — an auto-session has no answer to close over.
const (
	autoSessionKeyPrefix   = "mcp:"
	autoSessionOperationNS = "mcp-auto"
)

// autoSessionRetry salts the interaction idempotency key. Core resolves a
// replayed start key to the interaction it already owns without checking its
// lease, so reusing the key would hand back the same dead interaction. Only a
// different key can open a live one.
//
// The salt is the id of the interaction that went stale rather than a counter,
// so concurrent callers that all hit the same dead interaction derive the same
// replacement key and converge on one new interaction instead of racing.
type autoSessionRetry struct{ afterInteractionID string }

// autoSessionMaxRebuilds bounds how many dead generations one call will walk
// past. A connection accumulates one dead interaction per idle gap, and each
// rebuild costs two Core round trips, so the walk has to end somewhere; the
// remembered key means a healthy connection never walks at all.
const autoSessionMaxRebuilds = 3

// autoSessionKeys remembers the start key that last worked for an MCP session.
// Without it every call after the first rebuild would replay the base key, get
// the long-dead first interaction back, and pay a failed ensure before
// rebuilding again — and the walk would grow by one generation per idle gap.
//
// Sessions end without telling this service, so entries are aged out by
// rotation rather than deletion: the live map is retired to previous once it
// fills, bounding the whole cache at twice the cap.
var autoSessionKeys = &rotatingKeyCache{cap: 2048}

type rotatingKeyCache struct {
	mu       sync.Mutex
	cap      int
	live     map[string]string
	previous map[string]string
}

func (c *rotatingKeyCache) get(sessionID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key, ok := c.live[sessionID]; ok {
		return key, true
	}
	key, ok := c.previous[sessionID]
	return key, ok
}

func (c *rotatingKeyCache) put(sessionID, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live == nil {
		c.live = make(map[string]string, 64)
	}
	if len(c.live) >= c.cap {
		c.previous = c.live
		c.live = make(map[string]string, 64)
	}
	c.live[sessionID] = key
}

// isStaleAutoSession reports whether an auto session died between calls in a way
// the client cannot see or repair. Core declares an interaction terminal once
// its lease lapses, and caps a single interaction at 128 operations; a long-lived
// MCP connection reaches both in ordinary use.
func isStaleAutoSession(value *lifecycleError) bool {
	if value == nil {
		return false
	}
	switch value.Code {
	case "interaction_terminal", "operation_required", "conversation_closed", "conversation_expired":
		return true
	default:
		return false
	}
}

// resolveAutoSession derives a full bkn_context for a client that supplied none.
// The caller decides when to ask for a rebuild, because the staleness only shows
// up downstream, when the operation is ensured against Core.
func resolveAutoSession(
	ctx context.Context,
	client *bkntrace.LifecycleClient,
	req mcpsdk.CallToolRequest,
	arguments map[string]any,
	retry autoSessionRetry,
) (bknContext, *lifecycleError, error) {
	if client == nil || !client.Enabled() {
		return bknContext{}, lifecycleErrorPtr(bkntrace.APIError{
			Code: "feature_not_installed", Message: "BKN Trace Core is not configured",
			RequiredAction: "install_enterprise_implementation",
		}), nil
	}
	sessionID := autoSessionID(ctx)
	if sessionID == "" {
		return bknContext{}, &lifecycleError{
			Code:           "conversation_required",
			Message:        "conversation_id is required",
			RequiredAction: "create_conversation",
		}, nil
	}
	conversation, apiErr, err := client.EnsureCurrentConversation(ctx, autoSessionKeyPrefix+sessionID)
	if err != nil {
		return bknContext{}, nil, err
	}
	if apiErr != nil {
		return bknContext{}, lifecycleErrorPtr(*apiErr), nil
	}
	startKey := autoInteractionKey(sessionID, retry)
	interaction, apiErr, err := client.StartInteraction(
		ctx, conversation.ConversationID, startKey,
	)
	if err != nil {
		return bknContext{}, nil, err
	}
	if apiErr != nil {
		return bknContext{}, lifecycleErrorPtr(*apiErr), nil
	}
	autoSessionKeys.put(sessionID, startKey)
	return bknContext{
		ConversationID: conversation.ConversationID,
		InteractionID:  interaction.InteractionID,
		OperationKey:   autoOperationKey(req.Params.Name, arguments),
	}, nil, nil
}

// autoSessionID identifies the MCP connection. Streamable HTTP assigns one per
// initialize handshake, so a reconnect deliberately starts a new conversation
// rather than silently extending causality across a gap the client cannot see.
func autoSessionID(ctx context.Context) string {
	session := mcpserver.ClientSessionFromContext(ctx)
	if session == nil {
		return ""
	}
	return strings.TrimSpace(session.SessionID())
}

// autoInteractionKey picks the start key for this attempt: the one that last
// worked for the session, or a fresh salt derived from the interaction that just
// died. Walking forward from the dead one matters — the base key resolves to the
// first interaction ever opened on this connection for as long as the connection
// lives, so a salt fixed to it would only ever reach the second generation.
func autoInteractionKey(sessionID string, retry autoSessionRetry) string {
	if retry.afterInteractionID != "" {
		return autoSessionKeyPrefix + sessionID + ":after:" + retry.afterInteractionID
	}
	if remembered, ok := autoSessionKeys.get(sessionID); ok {
		return remembered
	}
	return autoSessionKeyPrefix + sessionID
}

// autoOperationKey makes a replayed identical call idempotent within the
// interaction, and keeps two different calls distinct. Core keys operations by
// this value, so it must not collide across tools.
func autoOperationKey(toolName string, arguments map[string]any) string {
	normalized := map[string]any{}
	keys := make([]string, 0, len(arguments))
	for key := range arguments {
		if key == "bkn_context" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		normalized[key] = arguments[key]
	}
	raw, _ := json.Marshal(map[string]any{"tool": toolName, "input": normalized})
	sum := sha256.Sum256(raw)
	return autoSessionOperationNS + ":" + toolName + ":" + hex.EncodeToString(sum[:8])
}

func lifecycleErrorPtr(value bkntrace.APIError) *lifecycleError {
	converted := lifecycleError(value)
	return &converted
}
