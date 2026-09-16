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

const knowledgeNetworkResourceType = "knowledge_network"

type AuthorizationResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AuthorizationResourceList struct {
	Entries []AuthorizationResource `json:"entries"`
	Total   int                     `json:"total"`
}

type AuthorizationResourceQuery struct {
	Name      string
	Sort      string
	Direction string
	Offset    int
	Limit     int
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

func NewAuthorizationResourceCatalog(upstream config.UpstreamConfig) (AuthorizationResourceCatalog, error) {
	provider, err := newKnowledgeNetworkProvider(upstream)
	if err != nil {
		return nil, err
	}
	return &authorizationResourceCatalog{providers: map[string]AuthorizationResourceProvider{
		knowledgeNetworkResourceType: provider,
	}}, nil
}

func (c *authorizationResourceCatalog) List(ctx context.Context, resourceType string, query AuthorizationResourceQuery) (AuthorizationResourceList, error) {
	provider, ok := c.providers[resourceType]
	if !ok {
		return AuthorizationResourceList{}, errUnsupportedResourceType
	}
	return provider.List(ctx, query)
}

var errUnsupportedResourceType = errors.New("unsupported authorization resource type")

type knowledgeNetworkProvider struct {
	endpoint string
	client   *http.Client
}

func newKnowledgeNetworkProvider(upstream config.UpstreamConfig) (*knowledgeNetworkProvider, error) {
	baseURL, err := url.ParseRequestURI(upstream.BaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("invalid bkn backend base URL")
	}
	timeout := upstream.Timeout
	if timeout <= 0 {
		return nil, fmt.Errorf("bkn backend timeout must be positive")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/api/bkn-backend/in/v1/authorization-resources"
	return &knowledgeNetworkProvider{endpoint: baseURL.String(), client: &http.Client{Timeout: timeout}}, nil
}

func (p *knowledgeNetworkProvider) List(ctx context.Context, query AuthorizationResourceQuery) (AuthorizationResourceList, error) {
	u, err := url.Parse(p.endpoint)
	if err != nil {
		return AuthorizationResourceList{}, err
	}
	values := u.Query()
	values.Set("sort", query.Sort)
	values.Set("direction", query.Direction)
	values.Set("offset", strconv.Itoa(query.Offset))
	values.Set("limit", strconv.Itoa(query.Limit))
	if query.Name != "" {
		values.Set("name", query.Name)
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
		return AuthorizationResourceList{}, fmt.Errorf("bkn backend returned status %d", response.StatusCode)
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
			if errors.Is(err, errUnsupportedResourceType) {
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
	return AuthorizationResourceQuery{Name: strings.TrimSpace(c.Query("name")), Sort: sort, Direction: direction, Offset: offset, Limit: limit}, true
}
