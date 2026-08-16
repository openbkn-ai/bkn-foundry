// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func BuildDslQuery(ctx context.Context, queryStr string, query *interfaces.ConceptsQuery) (map[string]any, error) {
	var dslMap map[string]any
	err := json.Unmarshal([]byte(queryStr), &dslMap)
	if err != nil {
		return map[string]any{}, rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_InternalError_UnMarshalDataFailed).
			WithErrorDetails(fmt.Sprintf("failed to unMarshal dslStr to map, %s", err.Error()))
	}

	// Process sort parameters.
	sort := []map[string]any{}
	for _, sp := range query.Sort {
		// Do not validate sort fields here. OpenSearch reports an error when a sort field does not exist.
		sort = append(sort, map[string]any{
			sp.Field: sp.Direction,
		})
	}

	dsl := map[string]any{
		"size":         query.Limit,
		"sort":         sort,
		"track_scores": true,
	}
	dsl["query"] = dslMap

	return dsl, nil
}

// PropertyIndexCaps describes the search capabilities a Vega resource field actually has in a local index.
// Capabilities come from field features in the resource schema, written by openbkn vega dataset build,
// rather than index_config manually entered on object-type properties.
type PropertyIndexCaps struct {
	Keyword  bool
	Fulltext bool
	Vector   bool
}

// VegaResourceIndexCaps derives index capabilities for each resource field, keyed by resource field name.
//
// It returns nil when the resource has no local index (index_name is empty). features only configures what to
// build; the capabilities do not exist until the build completes, and Vega queries the source database directly.
func VegaResourceIndexCaps(res *interfaces.VegaResource) map[string]PropertyIndexCaps {
	if res == nil || res.LocalIndexName == "" {
		return nil
	}

	caps := make(map[string]PropertyIndexCaps, len(res.SchemaDefinition))
	for _, p := range res.SchemaDefinition {
		if p == nil {
			continue
		}
		for _, feature := range p.Features {
			// A feature can be declared on one property and apply to another field through ref_property. Ownership must
			// match the algorithm used by vega-backend when it creates build-task snapshots, or capabilities attach to
			// the wrong field.
			field := p.Name
			if feature.RefProperty != "" {
				field = feature.RefProperty
			}
			propCaps := caps[field]
			switch feature.FeatureType {
			case interfaces.FieldFeatureType_Keyword:
				propCaps.Keyword = true
			case interfaces.FieldFeatureType_Fulltext:
				propCaps.Fulltext = true
			case interfaces.FieldFeatureType_Vector:
				propCaps.Vector = true
			default:
				continue
			}
			caps[field] = propCaps
		}
	}
	return caps
}

// VegaResourceSchemaToFieldsMap maps vega Resource schema to view-like fields for display and validation.
func VegaResourceSchemaToFieldsMap(res *interfaces.VegaResource) map[string]*interfaces.ViewField {
	fields := make(map[string]*interfaces.ViewField)
	for _, p := range res.SchemaDefinition {
		if p == nil {
			continue
		}
		fields[p.Name] = &interfaces.ViewField{
			Name:         p.Name,
			Type:         p.Type,
			DisplayName:  p.DisplayName,
			OriginalName: p.OriginalName,
		}
	}
	return fields
}
