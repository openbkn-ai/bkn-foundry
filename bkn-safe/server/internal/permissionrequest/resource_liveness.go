// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permissionrequest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
)

// ResourceLivenessResolver resolves whether the authoritative service still
// owns a resource. It is deliberately separate from authorization: a global
// administrator may retain grant permission after a resource has gone away.
type ResourceLivenessResolver interface {
	Exists(ctx context.Context, resourceType, resourceID string) (bool, error)
}

type httpResourceLivenessResolver struct {
	endpoints                    map[string]string
	clients                      map[string]*http.Client
	authorizationURL             string
	authorizationClient          *http.Client
	executionAuthorizationURL    string
	executionAuthorizationClient *http.Client
	accessorID                   string
}

// NewHTTPResourceLivenessResolver builds resolvers for resource types whose
// owning services already expose stable internal GET-by-ID endpoints.
func NewHTTPResourceLivenessResolver(bknBackend, executionFactory, vegaBackend config.UpstreamConfig, accessorID string) (ResourceLivenessResolver, error) {
	accessorID = strings.TrimSpace(accessorID)
	if accessorID == "" {
		return nil, fmt.Errorf("resource resolver accessor ID is required")
	}
	resolver := &httpResourceLivenessResolver{
		endpoints:  make(map[string]string),
		clients:    make(map[string]*http.Client),
		accessorID: accessorID,
	}
	if err := resolver.add(bknBackend, "bkn backend", "knowledge_network", "/api/bkn-backend/in/v1/knowledge-networks/"); err != nil {
		return nil, err
	}
	base, err := url.ParseRequestURI(bknBackend.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("invalid bkn backend base URL")
	}
	if bknBackend.Timeout <= 0 {
		return nil, fmt.Errorf("bkn backend timeout must be positive")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/api/bkn-backend/in/v1/authorization-resources"
	resolver.authorizationURL = base.String()
	resolver.authorizationClient = &http.Client{Timeout: bknBackend.Timeout}
	if err := resolver.add(vegaBackend, "vega backend", "catalog", "/api/vega-backend/in/v1/catalogs/"); err != nil {
		return nil, err
	}
	if err := resolver.add(vegaBackend, "vega backend", "resource", "/api/vega-backend/in/v1/resources/"); err != nil {
		return nil, err
	}
	if err := resolver.add(executionFactory, "execution factory", "tool_box", "/api/agent-operator-integration/internal-v1/tool-box/"); err != nil {
		return nil, err
	}
	if err := resolver.add(executionFactory, "execution factory", "mcp", "/api/agent-operator-integration/internal-v1/mcp/"); err != nil {
		return nil, err
	}
	executionBase, err := url.ParseRequestURI(executionFactory.BaseURL)
	if err != nil || executionBase.Scheme == "" || executionBase.Host == "" {
		return nil, fmt.Errorf("invalid execution factory base URL")
	}
	if executionFactory.Timeout <= 0 {
		return nil, fmt.Errorf("execution factory timeout must be positive")
	}
	executionBase.Path = strings.TrimRight(executionBase.Path, "/") + "/api/agent-operator-integration/internal-v1/authorization-resources"
	resolver.executionAuthorizationURL = executionBase.String()
	resolver.executionAuthorizationClient = &http.Client{Timeout: executionFactory.Timeout}
	return resolver, nil
}

func (r *httpResourceLivenessResolver) add(upstream config.UpstreamConfig, service, resourceType, path string) error {
	base, err := url.ParseRequestURI(upstream.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return fmt.Errorf("invalid %s base URL", service)
	}
	if upstream.Timeout <= 0 {
		return fmt.Errorf("%s timeout must be positive", service)
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	r.endpoints[resourceType] = base.String()
	r.clients[resourceType] = &http.Client{Timeout: upstream.Timeout}
	return nil
}

func (r *httpResourceLivenessResolver) Exists(ctx context.Context, resourceType, resourceID string) (bool, error) {
	if isKnowledgeNetworkChild(resourceType) {
		return r.existsChild(ctx, resourceType, resourceID)
	}
	if resourceType == "function" || resourceType == "skill" {
		return r.existsExecutionFactoryResource(ctx, resourceType, resourceID)
	}
	endpoint, ok := r.endpoints[resourceType]
	if !ok {
		return false, ErrUnsupportedResourceType
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+url.PathEscape(resourceID), nil)
	if err != nil {
		return false, err
	}
	// BKN Backend and Vega internal reads authorize the caller identified by
	// these headers. Execution Factory ignores them on its private read face.
	request.Header.Set("x-account-id", r.accessorID)
	request.Header.Set("x-account-type", "user")
	response, err := r.clients[resourceType].Do(request)
	if err != nil {
		return false, fmt.Errorf("query %s liveness: %w", resourceType, err)
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("query %s liveness: upstream returned status %d", resourceType, response.StatusCode)
	}
}

// Function and Skill have no private GET-by-ID endpoint. The execution
// factory's internal authorization-resource catalog is the authoritative
// read model used by bkn-safe's object-grant picker, so reuse it for a
// fail-closed existence check.
func (r *httpResourceLivenessResolver) existsExecutionFactoryResource(ctx context.Context, resourceType, resourceID string) (bool, error) {
	for offset := 0; ; offset += 100 {
		u, err := url.Parse(r.executionAuthorizationURL)
		if err != nil {
			return false, err
		}
		values := u.Query()
		values.Set("resource_type", resourceType)
		values.Set("sort", "name")
		values.Set("direction", "asc")
		values.Set("offset", strconv.Itoa(offset))
		values.Set("limit", "100")
		u.RawQuery = values.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return false, err
		}
		response, err := r.executionAuthorizationClient.Do(request)
		if err != nil {
			return false, fmt.Errorf("query %s liveness: %w", resourceType, err)
		}
		var page authorizationResourcePage
		decodeErr := json.NewDecoder(response.Body).Decode(&page)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return false, fmt.Errorf("query %s liveness: upstream returned status %d", resourceType, response.StatusCode)
		}
		if decodeErr != nil {
			return false, decodeErr
		}
		for _, entry := range page.Entries {
			if entry.ID == resourceID {
				return true, nil
			}
		}
		if len(page.Entries) == 0 || offset+len(page.Entries) >= page.Total {
			return false, nil
		}
	}
}

func isKnowledgeNetworkChild(resourceType string) bool {
	switch resourceType {
	case "concept_group", "object_type", "relation_type", "action_type", "metric":
		return true
	default:
		return false
	}
}

type authorizationResourcePage struct {
	Entries []struct {
		ID string `json:"id"`
	} `json:"entries"`
	Total int `json:"total"`
}

func (r *httpResourceLivenessResolver) existsChild(ctx context.Context, resourceType, resourceID string) (bool, error) {
	parts := strings.SplitN(strings.TrimSpace(resourceID), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false, ErrResourceUnavailable
	}
	for offset := 0; ; offset += 100 {
		u, err := url.Parse(r.authorizationURL)
		if err != nil {
			return false, err
		}
		values := u.Query()
		values.Set("resource_type", resourceType)
		values.Set("parent_type", "knowledge_network")
		values.Set("parent_id", parts[0])
		values.Set("sort", "name")
		values.Set("direction", "asc")
		values.Set("offset", strconv.Itoa(offset))
		values.Set("limit", "100")
		u.RawQuery = values.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return false, err
		}
		request.Header.Set("x-account-id", r.accessorID)
		request.Header.Set("x-account-type", "user")
		response, err := r.authorizationClient.Do(request)
		if err != nil {
			return false, fmt.Errorf("query %s liveness: %w", resourceType, err)
		}
		var page authorizationResourcePage
		decodeErr := json.NewDecoder(response.Body).Decode(&page)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return false, fmt.Errorf("query %s liveness: upstream returned status %d", resourceType, response.StatusCode)
		}
		if decodeErr != nil {
			return false, decodeErr
		}
		for _, entry := range page.Entries {
			if entry.ID == resourceID {
				return true, nil
			}
		}
		if len(page.Entries) == 0 || offset+len(page.Entries) >= page.Total {
			return false, nil
		}
	}
}
