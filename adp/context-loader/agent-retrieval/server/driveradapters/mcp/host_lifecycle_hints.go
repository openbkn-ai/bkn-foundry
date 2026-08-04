// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

const (
	hostConversationKeyHeader = "X-OpenBKN-Host-Conversation-Key"
	clientInvocationIDHeader  = common.HeaderBKNClientInvocationID
	hostConversationKeyMeta   = "openbkn.ai/host-conversation-key"
	clientInvocationIDMeta    = "openbkn.ai/client-invocation-id"
	maxHostLifecycleHintBytes = 256
)

type hostLifecycleHints struct {
	HostConversationKey string
	ClientInvocationID  string
}

func withMCPClientApplication(ctx context.Context) context.Context {
	session := server.ClientSessionFromContext(ctx)
	clientInfo, ok := session.(server.SessionWithClientInfo)
	if !ok {
		return ctx
	}
	return common.SetApplicationDisplayNameToCtx(ctx, clientInfo.GetClientInfo().Name)
}

func hostLifecycleHintsFromRequest(request mcpsdk.CallToolRequest) (hostLifecycleHints, error) {
	meta := map[string]any(nil)
	if request.Params.Meta != nil {
		meta = request.Params.Meta.AdditionalFields
	}
	hostKey, err := hostLifecycleHint(
		headerValue(request.Header, hostConversationKeyHeader), meta, hostConversationKeyMeta,
	)
	if err != nil {
		return hostLifecycleHints{}, err
	}
	invocationID, err := hostLifecycleHint(
		headerValue(request.Header, clientInvocationIDHeader), meta, clientInvocationIDMeta,
	)
	if err != nil {
		return hostLifecycleHints{}, err
	}
	return hostLifecycleHints{
		HostConversationKey: hostKey,
		ClientInvocationID:  invocationID,
	}, nil
}

func headerValue(header http.Header, name string) string {
	for key, values := range header {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func hostLifecycleHint(headerValue string, meta map[string]any, metaKey string) (string, error) {
	value := strings.TrimSpace(headerValue)
	if value == "" && meta != nil {
		raw, present := meta[metaKey]
		if present {
			text, ok := raw.(string)
			if !ok {
				return "", fmt.Errorf("%s must be a string", metaKey)
			}
			value = strings.TrimSpace(text)
		}
	}
	if value == "" {
		return "", nil
	}
	if len(value) > maxHostLifecycleHintBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", metaKey, maxHostLifecycleHintBytes)
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return "", fmt.Errorf("%s must be printable ASCII without spaces", metaKey)
		}
	}
	return value, nil
}
