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

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func BuildDslQuery(ctx context.Context, queryStr string, query *interfaces.ConceptsQuery) (map[string]any, error) {
	var dslMap map[string]any
	err := json.Unmarshal([]byte(queryStr), &dslMap)
	if err != nil {
		return map[string]any{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
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
// The local index must support queries before its configured features can be used.
func VegaResourceIndexCaps(res *interfaces.VegaResource) map[string]PropertyIndexCaps {
	if !vegaLocalIndexQueryable(res) {
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

// vegaLocalIndexQueryable reports a resource whose local index serves queries.
func vegaLocalIndexQueryable(res *interfaces.VegaResource) bool {
	return res != nil && (res.LocalIndexStatus == interfaces.ResourceLocalIndexStatusAvailable ||
		res.LocalIndexStatus == interfaces.ResourceLocalIndexStatusReadOnly)
}

// VegaPropertyRefusesConditions reports a resource field the resource data API refuses a
// comparison on: its filterable switch is off (#3365). A field with no switch is filterable.
//
// A text field of a dataset is the exception when it has a keyword subfield and the dataset's
// local index serves queries: a dataset is always read through that index, and vega-backend
// compares the field there and checks the subfield instead (filter_condition.FilterFieldForOperation).
// A table is not: Cypher reads it from the data source, where the field itself is checked and
// refused, so a table's field is judged by its own switch.
func VegaPropertyRefusesConditions(res *interfaces.VegaResource, p *interfaces.Property) bool {
	if p == nil || p.Filterable == nil || *p.Filterable {
		return false
	}
	if p.Type == "text" && res != nil && res.Category == interfaces.ResourceCategoryDataset && vegaLocalIndexQueryable(res) {
		for _, feature := range p.Features {
			if feature.FeatureType == interfaces.FieldFeatureType_Keyword {
				return false
			}
		}
	}
	return true
}

// VegaResourceSchemaToPropertiesMap maps a Vega Resource schema by property name.
func VegaResourceSchemaToPropertiesMap(res *interfaces.VegaResource) map[string]*interfaces.Property {
	properties := make(map[string]*interfaces.Property)
	for _, p := range res.SchemaDefinition {
		if p == nil {
			continue
		}
		properties[p.Name] = p
	}
	return properties
}
