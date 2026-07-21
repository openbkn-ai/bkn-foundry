// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package opensearch

import (
	"fmt"
	"strings"
)

var rawBucketAggregationTypes = map[string]struct{}{
	"terms":          {},
	"date_histogram": {},
}

var rawMetricAggregationTypes = map[string]struct{}{
	"value_count": {},
	"cardinality": {},
	"sum":         {},
	"avg":         {},
	"max":         {},
	"min":         {},
}

// RawAggregationValidationError identifies DSL that cannot be represented as
// the row-oriented aggregation result used by Resource Data queries.
type RawAggregationValidationError struct {
	Path   string
	Reason string
}

func (e *RawAggregationValidationError) Error() string {
	return fmt.Sprintf("invalid OpenSearch aggregation at %s: %s", e.Path, e.Reason)
}

type rawAggregationNode struct {
	name     string
	field    string
	operator string
	bucket   bool
	child    *rawAggregationNode
}

type rawAggregationPlan struct {
	root *rawAggregationNode
}

func compileRawAggregationPlan(query map[string]any) (*rawAggregationPlan, error) {
	aggregations, key, present, err := rawAggregationContainer(query, "query")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	root, err := compileRawAggregationNode(aggregations, key, make(map[string]struct{}))
	if err != nil {
		return nil, err
	}
	return &rawAggregationPlan{root: root}, nil
}

func compileRawAggregationNode(container map[string]any, path string, outputNames map[string]struct{}) (*rawAggregationNode, error) {
	if len(container) != 1 {
		return nil, rawAggregationError(path, "exactly one aggregation is required")
	}

	var name string
	var rawDefinition any
	for name, rawDefinition = range container {
	}
	definition, ok := rawDefinition.(map[string]any)
	if !ok {
		return nil, rawAggregationError(path+"."+name, "aggregation definition must be an object")
	}
	nodePath := path + "." + name

	operator := ""
	bucket := false
	for key := range definition {
		if _, ok := rawBucketAggregationTypes[key]; ok {
			if operator != "" {
				return nil, rawAggregationError(nodePath, "exactly one aggregation type is required")
			}
			operator = key
			bucket = true
			continue
		}
		if _, ok := rawMetricAggregationTypes[key]; ok {
			if operator != "" {
				return nil, rawAggregationError(nodePath, "exactly one aggregation type is required")
			}
			operator = key
			continue
		}
		if key != "aggs" && key != "aggregations" && key != "meta" {
			return nil, rawAggregationError(nodePath, fmt.Sprintf("unsupported aggregation type %q", key))
		}
	}
	if operator == "" {
		return nil, rawAggregationError(nodePath, "a supported aggregation type is required")
	}
	config, ok := definition[operator].(map[string]any)
	if !ok {
		return nil, rawAggregationError(nodePath+"."+operator, "aggregation configuration must be an object")
	}
	field, ok := config["field"].(string)
	if !ok || strings.TrimSpace(field) == "" {
		return nil, rawAggregationError(nodePath+"."+operator, "a non-empty field is required")
	}
	if bucket {
		if keyed, ok := config["keyed"].(bool); ok && keyed {
			return nil, rawAggregationError(nodePath+"."+operator, "keyed bucket responses are not supported")
		}
	}

	node := &rawAggregationNode{name: name, field: field, operator: operator, bucket: bucket}
	outputName := name
	if bucket {
		outputName = field
	}
	if _, exists := outputNames[outputName]; exists {
		return nil, rawAggregationError(nodePath, fmt.Sprintf("duplicate output field %q", outputName))
	}
	outputNames[outputName] = struct{}{}

	children, childKey, hasChildren, err := rawAggregationContainer(definition, nodePath)
	if err != nil {
		return nil, err
	}
	if hasChildren {
		if !bucket {
			return nil, rawAggregationError(nodePath, "metric aggregations cannot contain child aggregations")
		}
		node.child, err = compileRawAggregationNode(children, nodePath+"."+childKey, outputNames)
		if err != nil {
			return nil, err
		}
	} else if bucket {
		if _, exists := outputNames["__value"]; exists {
			return nil, rawAggregationError(nodePath, "output field __value conflicts with a group field")
		}
		outputNames["__value"] = struct{}{}
	}

	return node, nil
}

func rawAggregationContainer(source map[string]any, path string) (map[string]any, string, bool, error) {
	aggs, hasAggs := source["aggs"]
	aggregations, hasAggregations := source["aggregations"]
	if hasAggs && hasAggregations {
		return nil, "", false, rawAggregationError(path, "aggs and aggregations cannot both be present")
	}
	if !hasAggs && !hasAggregations {
		return nil, "", false, nil
	}
	key := "aggs"
	raw := aggs
	if hasAggregations {
		key = "aggregations"
		raw = aggregations
	}
	container, ok := raw.(map[string]any)
	if !ok || len(container) == 0 {
		return nil, "", false, rawAggregationError(path+"."+key, "must be a non-empty object")
	}
	return container, key, true, nil
}

func rawAggregationError(path, reason string) error {
	return &RawAggregationValidationError{Path: path, Reason: reason}
}

func (p *rawAggregationPlan) flatten(aggregations map[string]any) ([]map[string]any, error) {
	if p == nil || p.root == nil {
		return nil, nil
	}
	rootResult, ok := aggregations[p.root.name].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OpenSearch aggregation response is missing %s", p.root.name)
	}
	return p.root.flatten(rootResult, nil)
}

func (n *rawAggregationNode) flatten(result map[string]any, prefix map[string]any) ([]map[string]any, error) {
	if !n.bucket {
		value, ok := result["value"]
		if !ok {
			return nil, fmt.Errorf("OpenSearch metric aggregation response %s is missing value", n.name)
		}
		row := cloneRawAggregationRow(prefix)
		row[n.name] = value
		return []map[string]any{row}, nil
	}

	rawBuckets, ok := result["buckets"].([]any)
	if !ok {
		return nil, fmt.Errorf("OpenSearch bucket aggregation response %s is missing buckets", n.name)
	}
	rows := make([]map[string]any, 0, len(rawBuckets))
	for _, rawBucket := range rawBuckets {
		bucket, ok := rawBucket.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("OpenSearch bucket aggregation response %s contains an invalid bucket", n.name)
		}
		key, ok := bucket["key_as_string"]
		if !ok {
			key, ok = bucket["key"]
		}
		if !ok {
			return nil, fmt.Errorf("OpenSearch bucket aggregation response %s is missing a key", n.name)
		}
		row := cloneRawAggregationRow(prefix)
		row[n.field] = key
		if n.child == nil {
			docCount, ok := bucket["doc_count"]
			if !ok {
				return nil, fmt.Errorf("OpenSearch bucket aggregation response %s is missing doc_count", n.name)
			}
			row["__value"] = docCount
			rows = append(rows, row)
			continue
		}
		childResult, ok := bucket[n.child.name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("OpenSearch aggregation response is missing child %s", n.child.name)
		}
		childRows, err := n.child.flatten(childResult, row)
		if err != nil {
			return nil, err
		}
		rows = append(rows, childRows...)
	}
	return rows, nil
}

func cloneRawAggregationRow(source map[string]any) map[string]any {
	row := make(map[string]any, len(source)+1)
	for key, value := range source {
		row[key] = value
	}
	return row
}
