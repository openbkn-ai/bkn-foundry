package businessresolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ibusinessresolver"
)

type Resolver struct {
	bknBaseURL  string
	vegaBaseURL string
	client      *http.Client
}

type namedProperty struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type namedEntity struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Branch           string          `json:"branch"`
	Version          string          `json:"version"`
	DataProperties   []namedProperty `json:"data_properties"`
	LogicProperties  []namedProperty `json:"logic_properties"`
	SchemaDefinition []namedProperty `json:"schema_definition"`
}

type entriesResponse struct {
	Entries []namedEntity `json:"entries"`
}

func New(bknBaseURL, vegaBaseURL string, client *http.Client) *Resolver {
	if client == nil {
		client = http.DefaultClient
	}
	return &Resolver{bknBaseURL: strings.TrimRight(bknBaseURL, "/"), vegaBaseURL: strings.TrimRight(vegaBaseURL, "/"), client: client}
}

func (r *Resolver) ResolveBusinessRefs(ctx context.Context, request ibusinessresolver.ResolveRequest) ([]ibusinessresolver.Resolution, error) {
	cache := map[string]any{}
	result := make([]ibusinessresolver.Resolution, 0, len(request.Refs))
	for _, ref := range request.Refs {
		resolution, err := r.resolveOne(ctx, request.Scope, ref, cache)
		if err != nil {
			return nil, err
		}
		result = append(result, resolution)
	}
	return result, nil
}

func (r *Resolver) resolveOne(ctx context.Context, scope evidencevo.QueryScope, ref ibusinessresolver.BusinessRef, cache map[string]any) (ibusinessresolver.Resolution, error) {
	resolution := ibusinessresolver.Resolution{
		RefID: ref.RefID, RefType: ref.RefType, SourceSystem: ref.SourceSystem, Visibility: "unresolved",
	}
	parts := strings.Split(ref.RefID, ":")
	if len(parts) < 2 {
		return resolution, nil
	}
	kind, sourceSystem := resolverKind(ref)
	if kind == "" || parts[0] != kind || (ref.SourceSystem != "" && ref.SourceSystem != sourceSystem) {
		return resolution, nil
	}

	var entity namedEntity
	var path []string
	switch kind {
	case "kn":
		if len(parts) != 2 || r.bknBaseURL == "" {
			return resolution, nil
		}
		path = []string{"api", "bkn-backend", "in", "v1", "knowledge-networks", parts[1]}
		status, err := r.getJSON(ctx, scope, r.bknBaseURL, path, &entity, cache)
		return entityResolution(resolution, status, err, entity, []string{entity.Name})
	case "object", "relation", "action_type", "metric":
		if len(parts) != 3 || r.bknBaseURL == "" {
			return resolution, nil
		}
		segment := map[string]string{"object": "object-types", "relation": "relation-types", "action_type": "action-types", "metric": "metrics"}[kind]
		path = []string{"api", "bkn-backend", "in", "v1", "knowledge-networks", parts[1], segment, parts[2]}
		var response entriesResponse
		status, err := r.getJSON(ctx, scope, r.bknBaseURL, path, &response, cache)
		if len(response.Entries) > 0 {
			entity = response.Entries[0]
		}
		return entityResolution(resolution, status, err, entity, []string{entity.Name})
	case "property", "logic":
		if len(parts) != 4 || r.bknBaseURL == "" {
			return resolution, nil
		}
		path = []string{"api", "bkn-backend", "in", "v1", "knowledge-networks", parts[1], "object-types", parts[2]}
		var response entriesResponse
		status, err := r.getJSON(ctx, scope, r.bknBaseURL, path, &response, cache)
		if err != nil || status != http.StatusOK || len(response.Entries) == 0 {
			return entityResolution(resolution, status, err, namedEntity{}, nil)
		}
		entity = response.Entries[0]
		properties := entity.DataProperties
		if kind == "logic" {
			properties = entity.LogicProperties
		} else {
			properties = append(properties, entity.LogicProperties...)
		}
		for _, property := range properties {
			if property.Name == parts[3] {
				name := property.DisplayName
				if name == "" {
					name = property.Name
				}
				return resolved(resolution, name, []string{entity.Name, name}, entitySourceVersion(entity)), nil
			}
		}
		return resolution, nil
	case "resource", "field":
		if r.vegaBaseURL == "" || (kind == "resource" && len(parts) != 2) || (kind == "field" && len(parts) != 3) {
			return resolution, nil
		}
		resourceID := parts[1]
		path = []string{"api", "vega-backend", "in", "v1", "resources", resourceID}
		var response entriesResponse
		status, err := r.getJSON(ctx, scope, r.vegaBaseURL, path, &response, cache)
		if err != nil || status != http.StatusOK || len(response.Entries) == 0 {
			return entityResolution(resolution, status, err, namedEntity{}, nil)
		}
		entity = response.Entries[0]
		if kind == "resource" {
			return resolved(resolution, entity.Name, []string{entity.Name}, entitySourceVersion(entity)), nil
		}
		for _, property := range entity.SchemaDefinition {
			if property.Name == parts[2] {
				name := property.DisplayName
				if name == "" {
					name = property.Name
				}
				return resolved(resolution, name, []string{entity.Name, name}, entitySourceVersion(entity)), nil
			}
		}
	}
	return resolution, nil
}

func resolverKind(ref ibusinessresolver.BusinessRef) (string, string) {
	kinds := map[string]struct {
		kind   string
		source string
	}{
		"knowledge_network": {kind: "kn", source: "bkn"},
		"object_type":       {kind: "object", source: "bkn"},
		"object":            {kind: "object", source: "bkn"},
		"object_instance":   {kind: "object_instance", source: "bkn"},
		"property":          {kind: "property", source: "bkn"},
		"relation_type":     {kind: "relation", source: "bkn"},
		"relation":          {kind: "relation", source: "bkn"},
		"data_resource":     {kind: "resource", source: "vega"},
		"data_field":        {kind: "field", source: "vega"},
		"metric":            {kind: "metric", source: "bkn"},
		"logic":             {kind: "logic", source: "bkn"},
		"function":          {kind: "function", source: "bkn"},
		"action_type":       {kind: "action_type", source: "bkn"},
		"action_instance":   {kind: "action_instance", source: "bkn"},
	}
	if selected, found := kinds[ref.RefType]; found {
		return selected.kind, selected.source
	}
	if ref.RefType == "" {
		parts := strings.SplitN(ref.RefID, ":", 2)
		if len(parts) == 2 {
			for _, selected := range kinds {
				if selected.kind == parts[0] {
					return selected.kind, selected.source
				}
			}
		}
	}
	return "", ""
}

func entityResolution(base ibusinessresolver.Resolution, status int, err error, entity namedEntity, businessPath []string) (ibusinessresolver.Resolution, error) {
	if err != nil {
		return base, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		base.Visibility = "unauthorized"
		return base, nil
	}
	if status != http.StatusOK || entity.Name == "" {
		return base, nil
	}
	return resolved(base, entity.Name, businessPath, entitySourceVersion(entity)), nil
}

func entitySourceVersion(entity namedEntity) string {
	if branch := strings.TrimSpace(entity.Branch); branch != "" {
		return branch
	}
	return strings.TrimSpace(entity.Version)
}

func resolved(base ibusinessresolver.Resolution, name string, businessPath []string, version string) ibusinessresolver.Resolution {
	base.Visibility = "visible"
	base.Display = &evidencevo.BusinessDisplay{Name: name, BusinessPath: businessPath, ResolutionStatus: "resolved", SourceVersion: version}
	return base
}

func (r *Resolver) getJSON(ctx context.Context, scope evidencevo.QueryScope, baseURL string, path []string, target any, cache map[string]any) (int, error) {
	escaped := make([]string, len(path))
	for i, part := range path {
		escaped[i] = url.PathEscape(part)
	}
	endpoint := baseURL + "/" + strings.Join(escaped, "/")
	if cached, ok := cache[endpoint]; ok {
		body, _ := json.Marshal(cached)
		return http.StatusOK, json.Unmarshal(body, target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("x-account-id", scope.AccountID)
	req.Header.Set("x-account-type", scope.AccountType)
	if scope.Authorization != "" {
		req.Header.Set("Authorization", scope.Authorization)
	}
	if scope.TenantID != "" {
		req.Header.Set("x-tenant-id", scope.TenantID)
	}
	if scope.BusinessDomain != "" {
		req.Header.Set("x-business-domain", scope.BusinessDomain)
	}
	response, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode >= 500 {
			return response.StatusCode, fmt.Errorf("resolver upstream returned %d", response.StatusCode)
		}
		return response.StatusCode, nil
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return response.StatusCode, err
	}
	encoded, _ := json.Marshal(target)
	var cached any
	_ = json.Unmarshal(encoded, &cached)
	cache[endpoint] = cached
	return response.StatusCode, nil
}
