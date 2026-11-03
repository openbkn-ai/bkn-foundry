// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknbackend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// MountKnowledgeBases uses the existing idempotent capability mutation. Backend
// rechecks the publishing operator's source grant and updates the network proxy
// authorization. A 2xx reply alone is not acknowledgement of all requested IDs.
func (bba *bknBackendAccess) MountKnowledgeBases(ctx context.Context, token, network string, ids []string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "MountKnowledgeBases")
	defer span.End()
	if network == "" || strings.TrimSpace(token) == "" {
		return fmt.Errorf("knowledge-base mount identity is missing")
	}
	requested := slices.Clone(ids)
	slices.Sort(requested)
	requested = slices.Compact(requested)
	if len(requested) == 0 {
		return nil
	}
	type binding struct {
		ID           string `json:"id"`
		Type         string `json:"capability_type"`
		CapabilityID string `json:"capability_id"`
		Status       string `json:"status,omitempty"`
	}
	entries := []binding{}
	for _, id := range requested {
		if id == "" {
			return fmt.Errorf("knowledge-base identity is missing")
		}
		entries = append(entries, binding{Type: "knowledge_base", CapabilityID: id})
	}
	// Send only the capability references; binding ids/status are response fields.
	capabilities := []map[string]string{}
	for _, entry := range entries {
		capabilities = append(capabilities, map[string]string{"capability_type": entry.Type, "capability_id": entry.CapabilityID})
	}
	endpoint := bba.baseUrl + "/knowledge-networks/" + url.PathEscape(network) + "/capabilities?branch=main"
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: endpoint, HttpMethod: http.MethodPost})
	status, body, err := bba.httpClient.PostNoUnmarshal(ctx, endpoint, map[string]string{"Authorization": token, rest.ContentTypeKey: rest.ContentTypeJson}, map[string]any{"capabilities": capabilities})
	if err != nil {
		return fmt.Errorf("mount knowledge bases: %w", err)
	}
	if status != http.StatusOK {
		return backendError(status, body)
	}
	var reply struct {
		Entries []binding `json:"entries"`
	}
	if json.Unmarshal(body, &reply) != nil || len(reply.Entries) != len(requested) {
		return fmt.Errorf("incomplete knowledge-base mount acknowledgement")
	}
	seen := map[string]bool{}
	for _, entry := range reply.Entries {
		if entry.ID == "" || entry.Type != "knowledge_base" || entry.Status == "missing" || !slices.Contains(requested, entry.CapabilityID) || seen[entry.CapabilityID] {
			return fmt.Errorf("invalid knowledge-base mount acknowledgement")
		}
		seen[entry.CapabilityID] = true
	}
	return nil
}
