// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknbackend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Lists are read a page at a time up to maxListed entries: what a project shows of a network's
// implementations is a list to choose from, not the whole network.
const (
	listPageSize = 500
	maxListed    = 2000
)

type objectMetricRecord struct {
	Definition struct {
		ID                string `json:"id"`
		Version           int    `json:"version"`
		Code              string `json:"code"`
		Name              string `json:"name"`
		Description       string `json:"description"`
		OwnerObjectTypeID string `json:"owner_object_type_id"`
		MetricType        string `json:"metric_type"`
		Lifecycle         struct {
			Status string `json:"status"`
		} `json:"lifecycle"`
	} `json:"definition"`
	OwnerObjectTypeName string `json:"owner_object_type_name"`
}

func (r *objectMetricRecord) metric() *interfaces.KNObjectMetric {
	d := r.Definition
	return &interfaces.KNObjectMetric{ID: d.ID, Version: d.Version, Code: d.Code, Name: d.Name, Description: d.Description,
		OwnerObjectTypeID: d.OwnerObjectTypeID, OwnerObjectTypeName: r.OwnerObjectTypeName, MetricType: d.MetricType,
		Status: d.Lifecycle.Status}
}

// getJSON reads path under the network, in its draft, as the caller, into out: what is built in the
// workspace, metrics above all, is in the draft until it is published.
func (bba *bknBackendAccess) getJSON(ctx context.Context, name string, token string, knID string, path string,
	query url.Values, out any) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, name)
	defer span.End()
	span.SetAttributes(attr.Key("kn_id").String(knID))

	httpUrl := fmt.Sprintf("%s/%s%s", bba.networksUrl(), url.PathEscape(knID), path)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodGet})
	if query == nil {
		query = url.Values{}
	}
	query.Set("kn_version", interfaces.KN_VERSION_DRAFT)
	respCode, respBody, err := bba.httpClient.GetNoUnmarshal(ctx, httpUrl, query, map[string]string{"Authorization": token})
	logger.Debugf("%s finished, kn_id is [%s], response code is [%d]", name, knID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get failed")
		return fmt.Errorf("%s of network %s: %w", name, knID, err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return backendError(respCode, respBody)
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal failed")
		return fmt.Errorf("unmarshal %s of network %s: %w", name, knID, err)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}

// listPages reads a list a page at a time: page reads one at offset and returns how many it read.
func listPages(page func(offset int) (int, int, error)) error {
	for offset := 0; offset < maxListed; offset += listPageSize {
		n, total, err := page(offset)
		if err != nil {
			return err
		}
		// A list without a total ends at its first page that is not full.
		if n < listPageSize || total > 0 && offset+n >= total {
			return nil
		}
	}
	return nil
}

func pageQuery(offset int) url.Values {
	return url.Values{"offset": []string{strconv.Itoa(offset)}, "limit": []string{strconv.Itoa(listPageSize)}}
}

// ListObjectMetrics calls GET /knowledge-networks/{kn_id}/object-metrics.
func (bba *bknBackendAccess) ListObjectMetrics(ctx context.Context, token string, knID string,
	ownerObjectTypeID string) ([]*interfaces.KNObjectMetric, error) {
	metrics := []*interfaces.KNObjectMetric{}
	err := listPages(func(offset int) (int, int, error) {
		query := pageQuery(offset)
		if ownerObjectTypeID != "" {
			query.Set("owner_object_type_id", ownerObjectTypeID)
		}
		var page struct {
			Entries    []*objectMetricRecord `json:"entries"`
			TotalCount int                   `json:"total_count"`
		}
		if err := bba.getJSON(ctx, "ListObjectMetrics", token, knID, "/object-metrics", query, &page); err != nil {
			return 0, 0, err
		}
		for _, r := range page.Entries {
			metrics = append(metrics, r.metric())
		}
		return len(page.Entries), page.TotalCount, nil
	})
	return metrics, err
}

// GetObjectMetric calls GET /knowledge-networks/{kn_id}/object-metrics/{metric_id}.
func (bba *bknBackendAccess) GetObjectMetric(ctx context.Context, token string, knID string,
	metricID string) (*interfaces.KNObjectMetric, error) {
	var record objectMetricRecord
	if err := bba.getJSON(ctx, "GetObjectMetric", token, knID, "/object-metrics/"+url.PathEscape(metricID), nil, &record); err != nil {
		return nil, err
	}
	return record.metric(), nil
}

// ListCapabilities calls GET /knowledge-networks/{kn_id}/capabilities?with_detail=true.
func (bba *bknBackendAccess) ListCapabilities(ctx context.Context, token string, knID string, capabilityType string,
	metadataType string) ([]*interfaces.KNCapability, error) {
	capabilities := []*interfaces.KNCapability{}
	err := listPages(func(offset int) (int, int, error) {
		query := pageQuery(offset)
		query.Set("with_detail", "true")
		if capabilityType != "" {
			query.Set("type", capabilityType)
		}
		if metadataType != "" {
			query.Set("metadata_type", metadataType)
		}
		var page struct {
			Entries    []*interfaces.KNCapability `json:"entries"`
			TotalCount int                        `json:"total_count"`
		}
		if err := bba.getJSON(ctx, "ListCapabilities", token, knID, "/capabilities", query, &page); err != nil {
			return 0, 0, err
		}
		capabilities = append(capabilities, page.Entries...)
		return len(page.Entries), page.TotalCount, nil
	})
	return capabilities, err
}

// ListActionTypes calls GET /knowledge-networks/{kn_id}/action-types.
func (bba *bknBackendAccess) ListActionTypes(ctx context.Context, token string, knID string,
	objectTypeID string) ([]*interfaces.KNActionType, error) {
	actionTypes := []*interfaces.KNActionType{}
	err := listPages(func(offset int) (int, int, error) {
		query := pageQuery(offset)
		if objectTypeID != "" {
			query.Set("object_type_id", objectTypeID)
		}
		var page struct {
			Entries    []*interfaces.KNActionType `json:"entries"`
			TotalCount int                        `json:"total_count"`
		}
		if err := bba.getJSON(ctx, "ListActionTypes", token, knID, "/action-types", query, &page); err != nil {
			return 0, 0, err
		}
		actionTypes = append(actionTypes, page.Entries...)
		return len(page.Entries), page.TotalCount, nil
	})
	return actionTypes, err
}

// ListObjectTypeIDs calls GET /knowledge-networks/{kn_id}/object-types.
func (bba *bknBackendAccess) ListObjectTypeIDs(ctx context.Context, token string, knID string) ([]string, error) {
	ids := []string{}
	err := listPages(func(offset int) (int, int, error) {
		var page struct {
			Entries []struct {
				ID string `json:"id"`
			} `json:"entries"`
			TotalCount int `json:"total_count"`
		}
		if err := bba.getJSON(ctx, "ListObjectTypes", token, knID, "/object-types", pageQuery(offset), &page); err != nil {
			return 0, 0, err
		}
		for _, e := range page.Entries {
			ids = append(ids, e.ID)
		}
		return len(page.Entries), page.TotalCount, nil
	})
	return ids, err
}
