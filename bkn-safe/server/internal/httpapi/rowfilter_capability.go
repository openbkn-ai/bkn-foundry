// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	rowfiltersocket "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/rowfilter"
)

const rowFilterCapabilityPath = "/api/ontology-query/in/v1/row-filter-capabilities"

// NewRowFilterPublishedObjectTypeResolver connects the Core-owned row-filter
// management service to ontology-query's published-model capability contract.
// The upstream is mandatory: management writes must fail closed when it is
// unavailable, rather than accepting a policy which a read path cannot safely
// enforce.
func NewRowFilterPublishedObjectTypeResolver(upstream config.UpstreamConfig) (RowFilterPublishedObjectTypeResolver, error) {
	baseURL, err := url.ParseRequestURI(upstream.BaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("invalid ontology-query base URL")
	}
	if upstream.Timeout <= 0 {
		return nil, fmt.Errorf("ontology-query timeout must be positive")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + rowFilterCapabilityPath
	return &rowFilterPublishedObjectTypeResolver{endpoint: baseURL.String(), client: &http.Client{Timeout: upstream.Timeout}}, nil
}

type rowFilterPublishedObjectTypeResolver struct {
	endpoint string
	client   *http.Client
}

func (resolver *rowFilterPublishedObjectTypeResolver) ResolvePublishedObjectType(ctx context.Context, objectTypeRef string) (rowfiltersocket.PublishedObjectType, error) {
	payload, err := json.Marshal(struct {
		ObjectTypeRef string `json:"object_type_ref"`
	}{ObjectTypeRef: objectTypeRef})
	if err != nil {
		return rowfiltersocket.PublishedObjectType{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, resolver.endpoint, bytes.NewReader(payload))
	if err != nil {
		return rowfiltersocket.PublishedObjectType{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := resolver.client.Do(request)
	if err != nil {
		return rowfiltersocket.PublishedObjectType{}, fmt.Errorf("row-filter capability request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return rowfiltersocket.PublishedObjectType{}, fmt.Errorf("row-filter capability upstream returned status %d", response.StatusCode)
	}
	var capability rowfiltersocket.PublishedObjectType
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&capability); err != nil {
		return rowfiltersocket.PublishedObjectType{}, fmt.Errorf("decode row-filter capability: %w", err)
	}
	if capability.ObjectTypeRef != objectTypeRef {
		return rowfiltersocket.PublishedObjectType{}, fmt.Errorf("row-filter capability object type mismatch")
	}
	return capability, nil
}
