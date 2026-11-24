// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package opensearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/index"
)

var _ index.IndexSessionSnapshot = (*openSearchPointInTime)(nil)

type openSearchPointInTime struct {
	connector     *OpenSearchConnector
	pitID         string
	indexName     string
	keepAlive     string
	query         map[string]any
	resource      *interfaces.Resource
	rawFieldTypes map[string]string
}

func (c *OpenSearchConnector) OpenIndexRead(ctx context.Context, indexName string, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams, keepAlive time.Duration) (interfaces.QueryPageSession, error) {
	if params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil {
		return nil, interfaces.NewConditionBuildError("cursor paging does not support index aggregation queries")
	}
	if err := c.validateStoredFilterCondition(resource, params); err != nil {
		return nil, err
	}
	query, err := c.buildDocumentQuery(resource, params)
	if err != nil {
		return nil, err
	}
	snapshot := &openSearchPointInTime{
		connector: c,
		indexName: indexName,
		resource:  resource,
		query:     query,
	}
	return snapshot.open(ctx, keepAlive)
}

func (c *OpenSearchConnector) OpenRawIndexRead(ctx context.Context, resource *interfaces.Resource,
	query map[string]any, keepAlive time.Duration) (interfaces.QueryPageSession, error) {
	for _, option := range []string{"pit", "terminate_after"} {
		if _, supplied := query[option]; supplied {
			return nil, interfaces.NewConditionBuildError("%s is managed by cursor paging", option)
		}
	}
	if plan, err := compileRawAggregationPlan(query); err != nil {
		return nil, err
	} else if plan != nil {
		return nil, interfaces.NewConditionBuildError("cursor paging does not support index aggregation queries")
	}
	if resource == nil {
		return nil, interfaces.NewConditionBuildError("DSL queries require a Resource source index")
	}
	snapshot := &openSearchPointInTime{
		connector: c, indexName: resource.SourceIdentifier, query: query, rawFieldTypes: rawQueryFieldTypes(resource),
	}
	return snapshot.open(ctx, keepAlive)
}

func (s *openSearchPointInTime) open(ctx context.Context, keepAlive time.Duration) (interfaces.QueryPageSession, error) {
	if strings.TrimSpace(s.indexName) == "" || s.indexName == "_all" || strings.ContainsAny(s.indexName, "*,?/") {
		return nil, interfaces.NewConditionBuildError("cursor paging requires a specific index or alias")
	}
	if s.query["sort"] == nil || keepAlive <= 0 {
		return nil, interfaces.NewConditionBuildError("index cursor requires a sort and positive keep-alive")
	}
	if err := s.connector.Connect(ctx); err != nil {
		return nil, err
	}
	lease := strconv.FormatInt(keepAlive.Milliseconds(), 10) + "ms"
	path := "/" + url.PathEscape(s.indexName) + "/_search/point_in_time"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}
	req.URL.RawQuery = url.Values{
		"keep_alive":                 {lease},
		"allow_partial_pit_creation": {"false"},
	}.Encode()
	resp, err := s.connector.client.Perform(req)
	if err != nil {
		return nil, fmt.Errorf("open index snapshot: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("open index snapshot: HTTP %d", resp.StatusCode)
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			return nil, interfaces.NewSourceReadForbiddenError(err)
		}
		return nil, err
	}
	var opened opensearchapi.PointInTimeCreateResp
	if err := json.NewDecoder(resp.Body).Decode(&opened); err != nil {
		return nil, fmt.Errorf("decode index snapshot: %w", err)
	}
	if opened.PitID == "" {
		return nil, errors.New("index snapshot response has no PIT ID")
	}
	s.pitID = opened.PitID
	s.keepAlive = lease
	s.query = maps.Clone(s.query)
	delete(s.query, "search_after")
	if opened.Shards.Failed != 0 {
		return nil, errors.Join(errors.New("incomplete index snapshot"), s.Close())
	}
	offset, _ := openSearchInt64(s.query["from"])
	checkPageSize := s.rawFieldTypes == nil
	return index.NewIndexReadSession(s, offset, s.query["track_total_hits"] == true, checkPageSize), nil
}

func (s *openSearchPointInTime) SearchPage(ctx context.Context, limit int, after []any, trackTotal bool) (*interfaces.QueryPage, []any, error) {
	query := maps.Clone(s.query)
	query["pit"] = map[string]string{"id": s.pitID, "keep_alive": s.keepAlive}
	query["size"] = limit
	query["track_total_hits"] = trackTotal
	if limit == 0 || len(after) > 0 {
		delete(query, "from")
	}
	if len(after) > 0 {
		query["search_after"] = after
	}
	if s.rawFieldTypes != nil {
		result, err := s.connector.executeRawQuery(ctx, s.indexName, query, s.rawFieldTypes, true)
		if err != nil {
			return nil, nil, err
		}
		page := &interfaces.QueryPage{Entries: result.Entries, Columns: result.Columns, TotalCount: result.TotalCount}
		return page, result.SearchAfter, nil
	}
	result, err := s.connector.executeDocumentQuery(ctx, s.indexName, s.resource, query, true)
	if err != nil {
		return nil, nil, err
	}
	page := &interfaces.QueryPage{
		Entries:    result.Entries,
		TotalCount: &result.TotalCount,
	}
	return page, result.SearchAfter, nil
}

func (s *openSearchPointInTime) Close() error {
	req := opensearchapi.PointInTimeDeleteRequest{PitID: []string{s.pitID}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, result, err := req.Do(ctx, s.connector.client)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusNotFound {
			return nil
		}
		if resp.IsError() {
			return fmt.Errorf("close index snapshot: HTTP %d", resp.StatusCode)
		}
	}
	if err != nil {
		return fmt.Errorf("close index snapshot: %w", err)
	}
	if result == nil || len(result.Pits) != 1 || !result.Pits[0].Successful {
		return errors.New("index snapshot could not be closed")
	}
	return nil
}
