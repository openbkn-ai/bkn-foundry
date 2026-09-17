// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
)

const (
	knowledgeNetworkResourceType = "knowledge_network"
	catalogResourceType          = "catalog"
	resourceResourceType         = "resource"
	objectTypeResourceType       = "object_type"
	relationTypeResourceType     = "relation_type"
	actionTypeResourceType       = "action_type"
	metricResourceType           = "metric"
	conceptGroupResourceType     = "concept_group"
	toolBoxResourceType          = "tool_box"
	functionResourceType         = "function"
	mcpResourceType              = "mcp"
	skillResourceType            = "skill"
)

type AuthorizationResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AuthorizationResourceList struct {
	Entries []AuthorizationResource `json:"entries"`
	Total   int                     `json:"total"`
}

type AuthorizationResourceQuery struct {
	Name       string
	ParentType string
	ParentID   string
	Sort       string
	Direction  string
	Offset     int
	Limit      int
}

type AuthorizationResourceProvider interface {
	List(context.Context, AuthorizationResourceQuery) (AuthorizationResourceList, error)
}

type AuthorizationResourceCatalog interface {
	List(context.Context, string, AuthorizationResourceQuery) (AuthorizationResourceList, error)
}

type authorizationResourceCatalog struct {
	providers map[string]AuthorizationResourceProvider
}

func NewAuthorizationResourceCatalog(bknBackend, executionFactory, vegaBackend config.UpstreamConfig) (AuthorizationResourceCatalog, error) {
	knowledgeNetworks, err := newAuthorizationResourceProvider(bknBackend, "/api/bkn-backend/in/v1/authorization-resources", "bkn backend", knowledgeNetworkResourceType)
	if err != nil {
		return nil, err
	}
	catalogs, err := newAuthorizationResourceProvider(vegaBackend, "/api/vega-backend/in/v1/authorization-resources", "vega backend", catalogResourceType)
	if err != nil {
		return nil, err
	}
	resources, err := newAuthorizationResourceProvider(vegaBackend, "/api/vega-backend/in/v1/authorization-resources", "vega backend", resourceResourceType)
	if err != nil {
		return nil, err
	}
	objectTypes, err := newAuthorizationResourceProvider(bknBackend, "/api/bkn-backend/in/v1/authorization-resources", "bkn backend", objectTypeResourceType)
	if err != nil {
		return nil, err
	}
	relationTypes, err := newAuthorizationResourceProvider(bknBackend, "/api/bkn-backend/in/v1/authorization-resources", "bkn backend", relationTypeResourceType)
	if err != nil {
		return nil, err
	}
	actionTypes, err := newAuthorizationResourceProvider(bknBackend, "/api/bkn-backend/in/v1/authorization-resources", "bkn backend", actionTypeResourceType)
	if err != nil {
		return nil, err
	}
	metrics, err := newAuthorizationResourceProvider(bknBackend, "/api/bkn-backend/in/v1/authorization-resources", "bkn backend", metricResourceType)
	if err != nil {
		return nil, err
	}
	conceptGroups, err := newAuthorizationResourceProvider(bknBackend, "/api/bkn-backend/in/v1/authorization-resources", "bkn backend", conceptGroupResourceType)
	if err != nil {
		return nil, err
	}
	toolBoxes, err := newAuthorizationResourceProvider(executionFactory, "/api/agent-operator-integration/internal-v1/authorization-resources", "execution factory", toolBoxResourceType)
	if err != nil {
		return nil, err
	}
	functions, err := newAuthorizationResourceProvider(executionFactory, "/api/agent-operator-integration/internal-v1/authorization-resources", "execution factory", functionResourceType)
	if err != nil {
		return nil, err
	}
	mcp, err := newAuthorizationResourceProvider(executionFactory, "/api/agent-operator-integration/internal-v1/authorization-resources", "execution factory", mcpResourceType)
	if err != nil {
		return nil, err
	}
	skills, err := newAuthorizationResourceProvider(executionFactory, "/api/agent-operator-integration/internal-v1/authorization-resources", "execution factory", skillResourceType)
	if err != nil {
		return nil, err
	}
	return &authorizationResourceCatalog{providers: map[string]AuthorizationResourceProvider{
		knowledgeNetworkResourceType: knowledgeNetworks,
		catalogResourceType:          catalogs,
		resourceResourceType:         resources,
		objectTypeResourceType:       objectTypes,
		relationTypeResourceType:     relationTypes,
		actionTypeResourceType:       actionTypes,
		metricResourceType:           metrics,
		conceptGroupResourceType:     conceptGroups,
		toolBoxResourceType:          toolBoxes,
		functionResourceType:         functions,
		mcpResourceType:              mcp,
		skillResourceType:            skills,
	}}, nil
}

func (c *authorizationResourceCatalog) List(ctx context.Context, resourceType string, query AuthorizationResourceQuery) (AuthorizationResourceList, error) {
	provider, ok := c.providers[resourceType]
	if !ok {
		return AuthorizationResourceList{}, errUnsupportedResourceType
	}
	if query.ParentType != "" {
		expectedParentType := map[string]string{
			resourceResourceType:     catalogResourceType,
			objectTypeResourceType:   knowledgeNetworkResourceType,
			relationTypeResourceType: knowledgeNetworkResourceType,
			actionTypeResourceType:   knowledgeNetworkResourceType,
			metricResourceType:       knowledgeNetworkResourceType,
			conceptGroupResourceType: knowledgeNetworkResourceType,
		}[resourceType]
		if expectedParentType == "" || query.ParentType != expectedParentType {
			return AuthorizationResourceList{}, errInvalidAuthorizationResourceParent
		}
	}
	return provider.List(ctx, query)
}

var errUnsupportedResourceType = errors.New("unsupported authorization resource type")
var errInvalidAuthorizationResourceParent = errors.New("invalid authorization resource parent")

type authorizationResourceProvider struct {
	endpoint     string
	client       *http.Client
	resourceType string
}

func newAuthorizationResourceProvider(upstream config.UpstreamConfig, path, service string, resourceType ...string) (*authorizationResourceProvider, error) {
	baseURL, err := url.ParseRequestURI(upstream.BaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("invalid %s base URL", service)
	}
	timeout := upstream.Timeout
	if timeout <= 0 {
		return nil, fmt.Errorf("%s timeout must be positive", service)
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + path
	provider := &authorizationResourceProvider{endpoint: baseURL.String(), client: &http.Client{Timeout: timeout}}
	if len(resourceType) > 0 {
		provider.resourceType = resourceType[0]
	}
	return provider, nil
}

func (p *authorizationResourceProvider) List(ctx context.Context, query AuthorizationResourceQuery) (AuthorizationResourceList, error) {
	u, err := url.Parse(p.endpoint)
	if err != nil {
		return AuthorizationResourceList{}, err
	}
	values := u.Query()
	values.Set("sort", query.Sort)
	values.Set("direction", query.Direction)
	values.Set("offset", strconv.Itoa(query.Offset))
	values.Set("limit", strconv.Itoa(query.Limit))
	if p.resourceType != "" {
		values.Set("resource_type", p.resourceType)
	}
	if query.Name != "" {
		values.Set("name", query.Name)
	}
	if query.ParentType != "" {
		values.Set("parent_type", query.ParentType)
		values.Set("parent_id", query.ParentID)
	}
	u.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return AuthorizationResourceList{}, err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return AuthorizationResourceList{}, err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return AuthorizationResourceList{}, fmt.Errorf("authorization resource upstream returned status %d", response.StatusCode)
	}
	var result AuthorizationResourceList
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return AuthorizationResourceList{}, err
	}
	if result.Entries == nil {
		result.Entries = []AuthorizationResource{}
	}
	return result, nil
}

func registerAuthorizationResources(g *gin.RouterGroup, catalog AuthorizationResourceCatalog) {
	g.GET("/authorization-resources", func(c *gin.Context) {
		query, ok := parseAuthorizationResourceQuery(c)
		if !ok {
			return
		}
		result, err := catalog.List(c.Request.Context(), c.Query("resource_type"), query)
		if err != nil {
			if errors.Is(err, errUnsupportedResourceType) || errors.Is(err, errInvalidAuthorizationResourceParent) {
				replyPublicError(c, http.StatusBadRequest)
			} else {
				replyPublicError(c, http.StatusServiceUnavailable)
			}
			return
		}
		c.JSON(http.StatusOK, result)
	})
}

func parseAuthorizationResourceQuery(c *gin.Context) (AuthorizationResourceQuery, bool) {
	resourceType := c.Query("resource_type")
	if resourceType == "" {
		replyPublicError(c, http.StatusBadRequest)
		return AuthorizationResourceQuery{}, false
	}
	sort := c.DefaultQuery("sort", "name")
	direction := c.DefaultQuery("direction", "asc")
	offset, offsetErr := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if sort != "name" || (direction != "asc" && direction != "desc") || offsetErr != nil || limitErr != nil || offset < 0 || limit < 1 || limit > 100 {
		replyPublicError(c, http.StatusBadRequest)
		return AuthorizationResourceQuery{}, false
	}
	parentType := strings.TrimSpace(c.Query("parent_type"))
	parentID := strings.TrimSpace(c.Query("parent_id"))
	if (parentType == "") != (parentID == "") {
		replyPublicError(c, http.StatusBadRequest)
		return AuthorizationResourceQuery{}, false
	}
	return AuthorizationResourceQuery{Name: strings.TrimSpace(c.Query("name")), ParentType: parentType, ParentID: parentID, Sort: sort, Direction: direction, Offset: offset, Limit: limit}, true
}
