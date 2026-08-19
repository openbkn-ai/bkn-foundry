// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knresources provides the data layer "resource direct query" capability (out of the ontology): list_resources / describe_resource.
// Complementary to search_schema (ontology/semantic entry), both are fed to run_sql.
// Authorization is forced by the downstream vega by checking account view_detail in its /in resource endpoint (empty account fail-closed).
package knresources

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// The resource_id input parameter of ErrResourceIDRequired describe_resource is empty.
var ErrResourceIDRequired = errors.New("resource_id is required")

// ErrKnBackendUnavailable Querying by kn_id requires an ontology side dependency, but it is not injected.
var ErrKnBackendUnavailable = errors.New("knowledge network backend is not configured")

const (
	// dataSourceTypeResource is the only form in object type binding that can be directly mapped to vega resource.
	dataSourceTypeResource = "resource"
	// knResourceFetchConcurrency Gets the upper limit of concurrency of resources by id. Binding is on the order of dozens of tables.
	// The serialization will reach the second level; any higher concurrency will only put unnecessary pressure on vega.
	knResourceFetchConcurrency = 8
)

// ListResourcesReq list_resources input (shared by MCP tools and internal REST endpoints).
type ListResourcesReq struct {
	KnID      string `json:"kn_id"`      // Optional, limit resources bound to a certain knowledge network; catalog_id/offset/limit is ignored when present.
	CatalogID string `json:"catalog_id"` // Optional, limited to a certain catalog.
	Type      string `json:"type"`       // Optional, resource category (table/file/...), mapping vega category.
	Offset    int    `json:"offset"`     // Optional, page offset.
	Limit     int    `json:"limit"`      // Optional, paging size.
}

// UnresolvedBinding An object type binding that could not be resolved into a resource. The three causes are reported separately.
// Because what the caller has to do is completely different: to model/to rebind/to ask for permissions.
type UnresolvedBinding struct {
	ObjectTypeID string `json:"object_type_id"`
	ResourceID   string `json:"resource_id,omitempty"`
	SourceType   string `json:"source_type,omitempty"` // stale_binding: data_source.type of binding declaration.
	Reason       string `json:"reason,omitempty"`      // missing: Reason for downstream return.
}

// Lite resource entries for ResourceLite list_resources.
type ResourceLite struct {
	ResourceID string `json:"resource_id"`
	Name       string `json:"name"`
	Type       string `json:"type"` // Resource category (taken from vega category)
	Status     string `json:"status"`
	CatalogID  string `json:"catalog_id"`
}

// ListResourcesResp list_resources response.
// Unbound / StaleBinding / Missing may not be empty only when querying by kn_id.
type ListResourcesResp struct {
	Entries    []ResourceLite `json:"entries"`
	TotalCount int64          `json:"total_count"`
	// The Unbound object type is not bound to the data source at all (data_source is missing or the id is empty).
	Unbound []UnresolvedBinding `json:"unbound,omitempty"`
	// StaleBinding is bound to the obsolete data source form (such as data_view), not vega resource.
	StaleBinding []UnresolvedBinding `json:"stale_binding,omitempty"`
	// The resource_id bound to Missing cannot be retrieved: the resource has been deleted, or the calling account does not have permission.
	Missing []UnresolvedBinding `json:"missing,omitempty"`
}

// ColumnLite describe_resource's physical column (used for writing SQL).
type ColumnLite struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// DescribeResourceResp describe_resource response.
type DescribeResourceResp struct {
	ResourceID    string       `json:"resource_id"`
	ConnectorType string       `json:"connector_type"`
	Columns       []ColumnLite `json:"columns"`
}

// KnResourcesService direct query of data layer resources (list/describe), thin wrapper vega resource endpoint.
type KnResourcesService interface {
	ListResources(ctx context.Context, req *ListResourcesReq) (*ListResourcesResp, error)
	DescribeResource(ctx context.Context, resourceID string) (*DescribeResourceResp, error)
}

type knResourcesService struct {
	vega interfaces.DrivenVega
	bkn  interfaces.BknBackendAccess
}

var (
	once     sync.Once
	instance KnResourcesService
)

// NewKnResourcesService create KnResourcesService singleton.
func NewKnResourcesService() KnResourcesService {
	once.Do(func() {
		instance = &knResourcesService{
			vega: drivenadapters.NewVegaAccess(),
			bkn:  drivenadapters.NewBknBackendAccess(),
		}
	})
	return instance
}

// NewKnResourcesServiceWith injection dependency creation (for testing).
func NewKnResourcesServiceWith(vega interfaces.DrivenVega, bkn interfaces.BknBackendAccess) KnResourcesService {
	return &knResourcesService{vega: vega, bkn: bkn}
}

// ListResources lists queryable data resources (output condensed fields; type is vega category).
// When kn_id is provided, the ontology binding is performed and fetched directly by id. When kn_id is not provided, it is account-level resource pool paging.
func (s *knResourcesService) ListResources(ctx context.Context, req *ListResourcesReq) (*ListResourcesResp, error) {
	if req == nil {
		req = &ListResourcesReq{}
	}
	if knID := strings.TrimSpace(req.KnID); knID != "" {
		return s.listByKnowledgeNetwork(ctx, knID, strings.TrimSpace(req.Type))
	}
	vegaResp, err := s.vega.ListResources(ctx, &interfaces.VegaListResourcesReq{
		CatalogID: strings.TrimSpace(req.CatalogID),
		Category:  strings.TrimSpace(req.Type),
		Offset:    req.Offset,
		Limit:     req.Limit,
	})
	if err != nil {
		return nil, err
	}

	out := &ListResourcesResp{
		Entries:    make([]ResourceLite, 0, len(vegaResp.Entries)),
		TotalCount: vegaResp.TotalCount,
	}
	for _, r := range vegaResp.Entries {
		out.Entries = append(out.Entries, ResourceLite{
			ResourceID: r.ID,
			Name:       r.Name,
			Type:       r.Category,
			Status:     r.Status,
			CatalogID:  r.CatalogID,
		})
	}
	return out, nil
}

// listByKnowledgeNetwork lists the data resources bound to a knowledge network.
//
// Go to "Get the binding on the main body -> Get the resources one by one by ID", deliberately not touching the list endpoint of vega: that is the account level.
// For resource pool paging, the bound table is ranked thousands of places in the large pool according to update_time. Take any page and then.
// The intersection will be missed (#781). Direct access by id has nothing to do with the size of the pool.
//
// Binding is naturally on the order of dozens of tables, so it is returned all at once without paging; paging will only dig the same hole over again.
func (s *knResourcesService) listByKnowledgeNetwork(ctx context.Context, knID, typeFilter string) (*ListResourcesResp, error) {
	if s.bkn == nil {
		return nil, ErrKnBackendUnavailable
	}
	detail, err := s.bkn.GetKnowledgeNetworkDetail(ctx, knID)
	if err != nil {
		return nil, err
	}

	out := &ListResourcesResp{Entries: make([]ResourceLite, 0)}
	if detail == nil {
		return out, nil
	}

	// Binding diversion: Those that can be retrieved are arranged into targets (duplication is removed in order of object type, the output is stable), and those that cannot be retrieved are arranged.
	// It is divided into three fields according to the cause.
	type target struct {
		objectTypeID string
		resourceID   string
	}
	targets := make([]target, 0, len(detail.ObjectTypes))
	seen := make(map[string]struct{}, len(detail.ObjectTypes))
	for _, ot := range detail.ObjectTypes {
		if ot == nil {
			continue
		}
		ds := ot.DataSource
		if ds == nil || strings.TrimSpace(ds.ID) == "" {
			out.Unbound = append(out.Unbound, UnresolvedBinding{ObjectTypeID: ot.ID})
			continue
		}
		resourceID := strings.TrimSpace(ds.ID)
		// Empty types are treated as resource (the old data is not fully written); other non-resource forms (such as.
		// The obsolete data_view) cannot be used to adjust the resource endpoint of vega, and that path must be 500.
		if sourceType := strings.TrimSpace(ds.Type); sourceType != "" &&
			!strings.EqualFold(sourceType, dataSourceTypeResource) {
			out.StaleBinding = append(out.StaleBinding, UnresolvedBinding{
				ObjectTypeID: ot.ID,
				ResourceID:   resourceID,
				SourceType:   sourceType,
			})
			continue
		}
		if _, dup := seen[resourceID]; dup {
			continue // It is normal for multiple object types to share one table, and resources are only returned once.
		}
		seen[resourceID] = struct{}{}
		targets = append(targets, target{objectTypeID: ot.ID, resourceID: resourceID})
	}

	// Concurrent retrieval is limited. A single failure will only fall into missing, and the entire call cannot fail - a dangling binding.
	// It shouldn't drag down the other dozens of tables.
	type fetched struct {
		resource *interfaces.VegaResource
		err      error
	}
	results := make([]fetched, len(targets))
	slots := make(chan struct{}, knResourceFetchConcurrency)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, resourceID string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			res, err := s.vega.GetResource(ctx, resourceID)
			results[i] = fetched{resource: res, err: err}
		}(i, t.resourceID)
	}
	wg.Wait()

	var firstFetchErr error
	for i, t := range targets {
		r := results[i]
		if r.err != nil || r.resource == nil {
			if firstFetchErr == nil && r.err != nil {
				firstFetchErr = r.err
			}
			out.Missing = append(out.Missing, UnresolvedBinding{
				ObjectTypeID: t.objectTypeID,
				ResourceID:   t.resourceID,
				Reason:       unresolvedReason(r.err),
			})
			continue
		}
		if typeFilter != "" && !strings.EqualFold(r.resource.Category, typeFilter) {
			continue
		}
		resourceID := r.resource.ID
		if resourceID == "" {
			resourceID = t.resourceID
		}
		out.Entries = append(out.Entries, ResourceLite{
			ResourceID: resourceID,
			Name:       r.resource.Name,
			Type:       r.resource.Category,
			Status:     r.resource.Status,
			CatalogID:  r.resource.CatalogID,
		})
	}
	// There is binding, none is retrieved, and the failure is not caused by a single resource such as "this resource is gone/no permissions".
	// That basically leaves the entire game unavailable (vega hangs, ctx times out). At this time, "success + empty list" is returned.
	// The caller must regard "the backend is down" as "this network has no tables" - exactly what this issue wants to eliminate.
	// Dumb failure. Transparently transmit the first error so that the downstream status code and reason can surface.
	//
	// On the contrary, 404/403 still remains in missing: only one table is bound to a network, and this table happened to be deleted.
	// That's a solid modeling fact and shouldn't be disguised as a service failure.
	if len(targets) > 0 && len(out.Missing) == len(targets) && isDownstreamOutage(firstFetchErr) {
		return nil, firstFetchErr
	}
	out.TotalCount = int64(len(out.Entries))
	return out, nil
}

// isDownstreamOutage determines whether the failure to obtain resources belongs to "the entire downstream is unavailable", not this one.
// resources themselves. 404/403 is a single resource fact (deleted/unauthorized), and the rest (5xx, timeout, unable to connect,
// Naked errors other than HTTPError) are treated as unavailable - it is better to report an error than to disguise the failure as an empty list.
func isDownstreamOutage(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *infraErr.HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.HTTPCode {
		case http.StatusNotFound, http.StatusForbidden:
			return false
		}
	}
	return true
}

// unresolvedReason Pack downstream errors into one line and put them into missing.reason; if there are no errors, it means the resource is empty.
func unresolvedReason(err error) string {
	if err == nil {
		return "resource not found"
	}
	return err.Error()
}

// DescribeResource takes the physical schema + connector type of a single resource (for writing run_sql).
func (s *knResourcesService) DescribeResource(ctx context.Context, resourceID string) (*DescribeResourceResp, error) {
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return nil, ErrResourceIDRequired
	}

	res, err := s.vega.GetResource(ctx, resourceID)
	if err != nil {
		return nil, err
	}

	connectorType, err := s.vega.GetResourceConnectorType(ctx, resourceID)
	if err != nil {
		return nil, err
	}

	columns := make([]ColumnLite, 0, len(res.SchemaDefinition))
	for _, c := range res.SchemaDefinition {
		columns = append(columns, ColumnLite{
			Name:        c.Name,
			Type:        c.Type,
			Description: c.Description,
		})
	}

	return &DescribeResourceResp{
		ResourceID:    res.ID,
		ConnectorType: connectorType,
		Columns:       columns,
	}, nil
}
