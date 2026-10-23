// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"encoding/json"
	"sync"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
)

const maxObjectInstanceMetricBatch = 32

type objectInstanceMetricBatchKey struct {
	property string
	metricID string
	version  int
}

type objectInstanceMetricBatchItem struct {
	index       int
	identityKey string
	identity    map[string]any
}

// Batch only simple published instance metrics. A batched result is used only
// when every requested identity has exactly one result and execution is not
// partial; all other shapes retain the original per-object path.
func (ots *objectTypeService) batchObjectInstanceMetricProperties(ctx context.Context,
	query *interfaces.ObjectPropertyValueQuery, objects interfaces.Objects, datas []map[string]any,
	logicProperties map[string]*interfaces.LogicProperty, propertyNames map[string]bool,
) []map[string]bool {
	return ots.batchObjectInstanceMetricPropertiesWithSemaphore(ctx, query, objects, datas,
		logicProperties, propertyNames, make(chan struct{}, maxConcurrentLogicProperties), &sync.Mutex{})
}

func (ots *objectTypeService) batchObjectInstanceMetricPropertiesWithSemaphore(ctx context.Context,
	query *interfaces.ObjectPropertyValueQuery, objects interfaces.Objects, datas []map[string]any,
	logicProperties map[string]*interfaces.LogicProperty, propertyNames map[string]bool,
	semaphore chan struct{}, mu *sync.Mutex,
) []map[string]bool {
	completed := make([]map[string]bool, len(objects.Datas))
	if objects.ObjectType == nil || objects.ObjectType.DataSource == nil ||
		objects.ObjectType.DataSource.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE ||
		objects.ObjectType.DataSource.ID == "" || ots.omAccess == nil || ctx.Err() != nil {
		return completed
	}
	service := logics.ObjectMetricQueryService()
	if service == nil {
		return completed
	}
	groups := make(map[objectInstanceMetricBatchKey][]objectInstanceMetricBatchItem)
	for index, object := range objects.Datas {
		for name, value := range object {
			logical := logicProperties[name]
			if !propertyNames[name] || logical == nil || logical.Type != interfaces.PROPERTY_TYPE_METRIC ||
				logical.DataSource == nil || len(query.DynamicParams[name]) != 0 {
				continue
			}
			metric, ok := value.(interfaces.MetricProperty)
			if !ok || len(metric.DynamicParams) != 0 {
				continue
			}
			metricID, version, ok := parseObjectMetricV1Source(logical.DataSource.ID)
			if !ok {
				continue
			}
			metricQuery, err := buildObjectMetricQueryFromLogicProperty(*objects.ObjectType,
				metric.Parameters.Filters, interfaces.MetricPropertyDynamicParams{}, nil, nil)
			if err != nil || metricQuery.Filter != nil || len(metricQuery.GroupBy) != 0 ||
				len(metricQuery.InstanceIdentities) != 1 {
				continue
			}
			identity := metricQuery.InstanceIdentities[0]
			identityKey, ok := objectInstanceMetricBatchIdentityKey(identity)
			if !ok {
				continue
			}
			key := objectInstanceMetricBatchKey{property: name, metricID: metricID, version: version}
			groups[key] = append(groups[key], objectInstanceMetricBatchItem{
				index: index, identityKey: identityKey, identity: identity,
			})
		}
	}
	type metricVersion struct {
		id      string
		version int
	}
	batchable := make(map[metricVersion]bool)
	var wg sync.WaitGroup
batchLoop:
	for key, items := range groups {
		if len(items) < 2 || ctx.Err() != nil {
			continue
		}
		versionKey := metricVersion{id: key.metricID, version: key.version}
		allowed, checked := batchable[versionKey]
		if !checked {
			metricContext, err := ots.omAccess.GetObjectMetricExecutionContext(ctx, query.KNID, query.Branch,
				key.metricID, key.version)
			allowed = err == nil && objectInstanceMetricBatchable(metricContext, query.ObjectTypeID, key.metricID, key.version)
			batchable[versionKey] = allowed
		}
		if !allowed {
			continue
		}
		for start := 0; start < len(items) && ctx.Err() == nil; start += maxObjectInstanceMetricBatch {
			end := min(start+maxObjectInstanceMetricBatch, len(items))
			chunk := items[start:end]
			identities := make([]map[string]any, 0, len(chunk))
			seen := make(map[string]struct{}, len(chunk))
			for _, item := range chunk {
				if _, exists := seen[item.identityKey]; exists {
					continue
				}
				seen[item.identityKey] = struct{}{}
				identities = append(identities, item.identity)
			}
			if len(identities) < 2 {
				continue
			}
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				break batchLoop
			}
			wg.Add(1)
			go func(key objectInstanceMetricBatchKey, chunk []objectInstanceMetricBatchItem,
				identities []map[string]any, seen map[string]struct{}) {
				defer wg.Done()
				defer func() { <-semaphore }()
				result, err := service.QueryObjectMetricDataV1(ctx, query.KNID, query.Branch, key.metricID,
					key.version, &interfaces.ObjectMetricQueryRequestV1{InstanceIdentities: identities})
				if err != nil || result.Status == "partial" || len(result.Warnings) != 0 ||
					result.NextCursor != "" || len(result.Entries) != len(identities) ||
					result.Model.OutputShape != "object_value" {
					return
				}
				entries := make(map[string]interfaces.ObjectMetricDataEntryV1, len(result.Entries))
				valid := true
				for _, entry := range result.Entries {
					identityKey, ok := objectInstanceMetricBatchIdentityKey(entry.InstanceIdentity)
					if !ok {
						valid = false
						break
					}
					if _, requested := seen[identityKey]; !requested {
						valid = false
						break
					}
					if _, duplicate := entries[identityKey]; duplicate {
						valid = false
						break
					}
					entries[identityKey] = entry
				}
				if !valid || len(entries) != len(identities) {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				for _, item := range chunk {
					individual := result
					individual.Entries = []interfaces.ObjectMetricDataEntryV1{entries[item.identityKey]}
					datas[item.index][key.property] = objectMetricDataToLogicPropertyValue(individual, nil)
					if completed[item.index] == nil {
						completed[item.index] = make(map[string]bool)
					}
					completed[item.index][key.property] = true
				}
			}(key, chunk, identities, seen)
		}
	}
	wg.Wait()
	return completed
}

func objectInstanceMetricBatchable(context *interfaces.ObjectMetricExecutionContextV1,
	objectTypeID, metricID string, version int) bool {
	if context == nil || context.Root == nil {
		return false
	}
	definition := &context.Root.Definition
	if definition.ID != metricID || definition.Version != version || definition.OwnerObjectTypeID != objectTypeID ||
		definition.MetricType != "atomic" || definition.CalculationScope != "object_instance" ||
		definition.Specification.Measure == nil ||
		len(definition.ResultSemantics.StatisticalGrain.GroupByDimensions) != 0 ||
		definition.ResultSemantics.StatisticalGrain.TimeGranularity != "" {
		return false
	}
	if qualifiers := definition.Specification.BusinessQualifiers; qualifiers != nil {
		if qualifiers.RelationPath != nil || qualifiers.TimeWindow != nil && qualifiers.TimeWindow.Mode != "unbounded" {
			return false
		}
	}
	return true
}

func objectInstanceMetricBatchIdentityKey(identity map[string]any) (string, bool) {
	if len(identity) == 0 {
		return "", false
	}
	for name, value := range identity {
		text, ok := value.(string)
		if !ok || !utf8.ValidString(name) || !utf8.ValidString(text) {
			return "", false
		}
	}
	encoded, err := json.Marshal(identity)
	return string(encoded), err == nil
}
