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

// autoSessionEpoch salts the interaction idempotency key. It advances only when
// a lease has expired, so the retry opens a fresh interaction instead of
// replaying the dead one.
type autoSessionRetry struct{ epoch int }

// resolveAutoSession derives a bkn_context for a client that supplied none, and
// rebuilds the session once when the previous interaction went stale. Core
// renews the lease on every operation, so only an idle connection lands here.
func resolveAutoSession(
	ctx context.Context,
	client *bkntrace.LifecycleClient,
	req mcpsdk.CallToolRequest,
	arguments map[string]any,
) (bknContext, *lifecycleError, error) {
	if client == nil || !client.Enabled() {
		return bknContext{}, lifecycleErrorPtr(bkntrace.APIError{
			Code: "feature_not_installed", Message: "BKN Trace Core is not configured",
			RequiredAction: "install_enterprise_implementation",
		}), nil
	}
	resolved, lifecycleErr, err := resolveAutoContext(ctx, client, req, arguments, autoSessionRetry{})
	if err != nil || !isAutoSessionRecoverable(lifecycleErr) {
		return resolved, lifecycleErr, err
	}
	return resolveAutoContext(ctx, client, req, arguments, autoSessionRetry{epoch: 1})
}

// resolveAutoContext derives a full bkn_context for a client that supplied none.
// It returns the zero value untouched when the client did supply one — a partial
// context stays an error, since splicing a client conversation onto a generated
// interaction would fabricate a causality edge that never happened.
func resolveAutoContext(
	ctx context.Context,
	client *bkntrace.LifecycleClient,
	req mcpsdk.CallToolRequest,
	arguments map[string]any,
	retry autoSessionRetry,
) (bknContext, *lifecycleError, error) {
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
	interaction, apiErr, err := client.StartInteraction(
		ctx, conversation.ConversationID, autoInteractionKey(sessionID, retry.epoch),
	)
	if err != nil {
		return bknContext{}, nil, err
	}
	if apiErr != nil {
		return bknContext{}, lifecycleErrorPtr(*apiErr), nil
	}
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

func autoInteractionKey(sessionID string, epoch int) string {
	if epoch <= 0 {
		return autoSessionKeyPrefix + sessionID
	}
	return autoSessionKeyPrefix + sessionID + ":" + string(rune('0'+epoch%10))
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

// isAutoSessionRecoverable reports whether a stale auto-session can be rebuilt
// by opening a new interaction. An idle connection loses its lease after five
// minutes, and the client has no way to know that happened.
func isAutoSessionRecoverable(value *lifecycleError) bool {
	if value == nil {
		return false
	}
	switch value.Code {
	case "interaction_terminal", "interaction_required", "conversation_closed", "conversation_expired":
		return true
	default:
		return false
	}
}

func lifecycleErrorPtr(value bkntrace.APIError) *lifecycleError {
	converted := lifecycleError(value)
	return &converted
}
