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
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"ontology-query/common"
	cond "ontology-query/common/condition"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	dtype "ontology-query/interfaces/data_type"
	"ontology-query/locale"
)

// Build the default sort for views.
func BuildViewSort(objectType interfaces.ObjectType) []*interfaces.SortParams {
	sorts := []*interfaces.SortParams{
		{
			Field:     interfaces.SORT_FIELD_SCORE,
			Direction: interfaces.DESC_DIRECTION,
		},
	}
	// Property-to-view mapping. Primary keys are property names, so find the primary key property and then its mapped view field name.
	propFieldMap := map[string]string{}
	for _, prop := range objectType.DataProperties {
		propFieldMap[prop.Name] = prop.MappedField.Name
	}

	for _, pri := range objectType.PrimaryKeys {
		if fieldName, exists := propFieldMap[pri]; exists {
			// If the field mapped from the primary key is empty, do not add sorting.
			if fieldName != "" {
				// If a mapping exists, assemble it into object properties.
				sorts = append(sorts, &interfaces.SortParams{
					Field:     pri,
					Direction: interfaces.ASC_DIRECTION,
				})
			}
		}
	}
	return sorts
}

// MapSortFieldsForDataView maps sort fields from object-type data property names to view column names
// (MappedField.Name) for virtualized data-view queries. SORT_FIELD_SCORE is passed through unchanged.
func MapSortFieldsForDataView(ctx context.Context, sort []*interfaces.SortParams, objectType interfaces.ObjectType) ([]*interfaces.SortParams, error) {
	if len(sort) == 0 {
		return sort, nil
	}
	propNameToViewField := make(map[string]string, len(objectType.DataProperties))
	for _, prop := range objectType.DataProperties {
		propNameToViewField[prop.Name] = prop.MappedField.Name
	}
	out := make([]*interfaces.SortParams, len(sort))
	for i, sp := range sort {
		if sp == nil {
			return nil, fmt.Errorf("%s", locale.ValidationDetail(ctx, "SortConfigRequired", nil))
		}
		field := sp.Field
		if field == interfaces.SORT_FIELD_SCORE {
			out[i] = &interfaces.SortParams{Field: field, Direction: sp.Direction}
			continue
		}
		viewField, ok := propNameToViewField[field]
		if !ok {
			return nil, fmt.Errorf("%s", locale.ValidationDetail(ctx, "SortPropertyInvalid", map[string]any{"field": field}))
		}
		if viewField == "" {
			return nil, fmt.Errorf("%s", locale.ValidationDetail(ctx, "SortMappedFieldRequired", map[string]any{"field": field}))
		}
		out[i] = &interfaces.SortParams{Field: viewField, Direction: sp.Direction}
	}
	return out, nil
}

// Build the default sort for object indexes.
func BuildIndexSort(objectType interfaces.ObjectType, propMap map[string]cond.DataProperty) []*interfaces.SortParams {
	sorts := []*interfaces.SortParams{
		{
			Field:     interfaces.SORT_FIELD_SCORE,
			Direction: interfaces.DESC_DIRECTION,
		},
	}

	for _, pri := range objectType.PrimaryKeys {
		// If a mapping exists, assemble it into object properties. The field also needs a keyword index to be sortable; otherwise it is excluded from sorting.

		// For text fields, check whether a keyword index is configured underneath; use xxx.keyword for sorting when present, otherwise exclude it from sorting.
		// String fields support sorting directly. If full-text indexing is enabled, text exists under the field's keyword.
		if propMap[pri].Type == dtype.DATATYPE_TEXT {
			if propMap[pri].IndexConfig != nil && propMap[pri].IndexConfig.KeywordConfig.Enabled {
				sorts = append(sorts, &interfaces.SortParams{
					Field:     pri + "." + dtype.KEYWORD_SUFFIX,
					Direction: interfaces.ASC_DIRECTION,
				})
			}
		} else {
			sorts = append(sorts, &interfaces.SortParams{
				Field:     pri,
				Direction: interfaces.ASC_DIRECTION,
			})
		}

	}
	return sorts
}

// Build a path key for cycle detection.
func BuildPathKey(path interfaces.RelationPath, nextNodeID string) string {
	key := fmt.Sprintf("%s:%s", path.Relations[0].RelationTypeId, path.Relations[0].SourceObjectId)
	for i := 0; i < len(path.Relations); i++ {
		key += fmt.Sprintf("->%s", path.Relations[i].TargetObjectId)
	}
	key += fmt.Sprintf("->%s", nextNodeID)
	return key
}

// Filter valid paths by excluding paths that contain cycles.
func FilterValidPaths(paths []interfaces.RelationPath, visitedNodes map[string]bool) []interfaces.RelationPath {
	var validPaths []interfaces.RelationPath

	for _, path := range paths {
		if IsPathValid(path, visitedNodes) {
			validPaths = append(validPaths, path)
		}
	}

	return validPaths
}

// Check whether the path is valid and acyclic.
func IsPathValid(path interfaces.RelationPath, visitedNodes map[string]bool) bool {
	if len(path.Relations) == 0 {
		return true
	}

	nodeSet := make(map[string]bool)
	var prevTargetId string

	// Check whether each node in the path is unique and whether the path is continuous.
	for i, relation := range path.Relations {
		// Check path continuity: except for the first edge, the previous edge's target node should equal the current edge's source node.
		if i > 0 {
			if prevTargetId != relation.SourceObjectId {
				logger.Debugf("检测到路径不连续 - 前一条边的目标节点[%s]不等于当前边的源节点[%s]", prevTargetId, relation.SourceObjectId)
				return false
			}
		}

		// For the first edge, check whether the source node has already been visited; it should not repeat.
		if i == 0 {
			if nodeSet[relation.SourceObjectId] {
				logger.Debugf("检测到路径起始节点重复: %s", relation.SourceObjectId)
				return false
			}
			nodeSet[relation.SourceObjectId] = true
		}

		// For all edges, check whether the target node has already been visited; if so, a cycle has formed.
		if nodeSet[relation.TargetObjectId] {
			logger.Debugf("检测到循环路径 - 重复节点: %s", relation.TargetObjectId)
			return false
		}
		nodeSet[relation.TargetObjectId] = true

		// Check for conflicts with visited nodes when visitedNodes is provided.
		if visitedNodes != nil {
			if visitedNodes[relation.SourceObjectId] || visitedNodes[relation.TargetObjectId] {
				logger.Debugf("检测到路径与已访问节点冲突")
				return false
			}
		}

		prevTargetId = relation.TargetObjectId
	}

	return true
}

// Check limits.
func CanGenerate(quotaManager *interfaces.PathQuotaManager, pathID int) bool {
	if quotaManager == nil {
		return true
	}

	// Check the global limit.
	currentGlobal := atomic.LoadInt64(&quotaManager.GlobalCount)
	if currentGlobal >= quotaManager.TotalLimit {
		logger.Debugf("达到全局限制: %d/%d", currentGlobal, quotaManager.TotalLimit)
		return false
	}

	used := 0
	if value, exist := quotaManager.UsedQuota.Load(pathID); !exist {
		quotaManager.UsedQuota.Store(pathID, used)
	} else {
		used = value.(int)
	}

	if quotaManager.RequestPathTypeNum > 1 {
		// Dynamic quota: calculate from weight and remaining total.
		maxQuota := quotaManager.TotalLimit - quotaManager.GlobalCount
		return int64(used) < maxQuota
	} else {
		// The total is below the limit, so more can be added.
		return quotaManager.GlobalCount < quotaManager.TotalLimit
	}
}

// RecordGenerated records generated paths.
func RecordGenerated(quotaManager *interfaces.PathQuotaManager, typePathID int, cnt int) {
	if quotaManager == nil {
		return
	}

	// Atomically increment the global count.
	newGlobalCount := atomic.AddInt64(&quotaManager.GlobalCount, int64(cnt))

	// Update quota usage for a specific path type.
	if value, exist := quotaManager.UsedQuota.Load(typePathID); !exist {
		quotaManager.UsedQuota.Store(typePathID, cnt)
	} else {
		quotaManager.UsedQuota.Store(typePathID, value.(int)+cnt)
	}
	logger.Debugf("路径配额更新 - 路径ID: %d, 新增: %d, 全局计数: %d/%d",
		typePathID, cnt, newGlobalCount, quotaManager.TotalLimit)
}

// Extract the object ID from object data.
func GetObjectID(objectData map[string]any, objectType *interfaces.ObjectType) (string, map[string]any) {
	if objectType == nil || len(objectType.PrimaryKeys) == 0 {
		return "", nil
	}

	// Build the object ID from the primary key.
	var idParts []string
	uk := map[string]any{}
	for _, pk := range objectType.PrimaryKeys {
		if value, exists := objectData[pk]; exists {
			idParts = append(idParts, fmt.Sprintf("%v", value))
			uk[pk] = value
		} else {
			idParts = append(idParts, "__NULL__")
		}
	}

	if len(idParts) == 0 {
		return "", uk
	}

	return objectType.OTID + "-" + strings.Join(idParts, "_"), uk
}

// Build batch conditions for direct mappings.
func BuildDirectBatchConditions(currentLevelObjects []interfaces.LevelObject,
	edge *interfaces.TypeEdge, isForward bool) ([]*cond.CondCfg, error) {

	var conditions []*cond.CondCfg
	var inValues []any
	var inField string
	mappingRules := edge.RelationType.MappingRules.([]interfaces.Mapping)

	for _, levelObj := range currentLevelObjects {
		// Build filter clauses by association relationship.
		// For multi-field associations, join each object's filter conditions with AND, then join filters for different objects with OR.
		// For one association field, use an IN operation for filters across multiple objects.
		objectConditions, targetField, inValue := BuildCondition(nil, mappingRules, isForward, levelObj.ObjectData)
		if inValue != nil {
			inValues = append(inValues, inValue)
		}
		inField = targetField

		// If an object has multiple filter clauses, join them with AND.
		if len(objectConditions) > 1 {
			conditions = append(conditions, &cond.CondCfg{
				Operation: "and",
				SubConds:  objectConditions,
			})
		} else if len(objectConditions) == 1 {
			conditions = append(conditions, objectConditions[0])
		}
	}

	if len(mappingRules) == 1 && len(inValues) > 0 {
		return []*cond.CondCfg{
			{
				Name:      inField,
				Operation: "in",
				ValueOptCfg: cond.ValueOptCfg{
					ValueFrom: "const",
					Value:     inValues,
				},
			},
		}, nil
	}

	return conditions, nil
}

func BuildCondition(viewQuery *interfaces.ViewQuery, mappingRules []interfaces.Mapping,
	isForward bool, currentObjectData map[string]any) ([]*cond.CondCfg, string, any) {

	conditions := []*cond.CondCfg{}
	var inValue any
	var targetField string
	// When a view acts as an intermediate table, query view data by _score desc and association fields asc.
	sort := []*interfaces.SortParams{
		{
			Field:     interfaces.SORT_FIELD_SCORE,
			Direction: interfaces.DESC_DIRECTION,
		},
	}

	for _, mapping := range mappingRules {
		// Use the forward direction by default; if reverse, rewrite source and target fields.
		targetName := mapping.TargetProp.Name
		sourceName := mapping.SourceProp.Name
		if !isForward {
			targetName = mapping.SourceProp.Name
			sourceName = mapping.TargetProp.Name
		}
		// For one association field, use field values as the IN filter condition.
		if len(mappingRules) == 1 {
			inValue = currentObjectData[sourceName]
			targetField = targetName
		}
		// For multi-field associations, build multiple filter conditions and join them with AND at the upper layer.
		conditions = append(conditions, &cond.CondCfg{
			Name:      targetName, // Pay attention to the difference between forward and reverse directions.
			Operation: "==",       // Relationships only support equality here.
			ValueOptCfg: cond.ValueOptCfg{
				ValueFrom: "const",
				Value:     currentObjectData[sourceName], // Source property obtained from the source object.
			},
		})
		sort = append(sort, &interfaces.SortParams{
			Field:     targetName,
			Direction: interfaces.ASC_DIRECTION,
		})
	}

	if viewQuery != nil {
		if len(conditions) > 0 {
			viewQuery.Filters = &cond.CondCfg{
				Operation: "and",
				SubConds:  conditions,
			}
		}
		viewQuery.Sort = sort
	}

	return conditions, targetField, inValue
}

// Check direct mapping conditions.
func CheckDirectMappingConditions(currentObjectData map[string]any,
	nextObject map[string]any, mappingRules []interfaces.Mapping, isForward bool) bool {

	for _, mapping := range mappingRules {
		var sourceValue, targetValue interface{}
		var ok bool

		if isForward {
			// Forward：currentObject -> nextObject.
			sourceValue, ok = currentObjectData[mapping.SourceProp.Name]
			if !ok {
				return false
			}
			targetValue, ok = nextObject[mapping.TargetProp.Name]
			if !ok {
				return false
			}
		} else {
			// Reverse：nextObject -> currentObject.
			sourceValue, ok = nextObject[mapping.SourceProp.Name]
			if !ok {
				return false
			}
			targetValue, ok = currentObjectData[mapping.TargetProp.Name]
			if !ok {
				return false
			}
		}

		// Compare whether values are equal.
		if !CompareValues(sourceValue, targetValue) {
			return false
		}
	}

	return true
}

// Compare whether two values are equal while handling different types.
func CompareValues(a, b interface{}) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Convert to strings for comparison to avoid type mismatch issues.
	aStr := fmt.Sprintf("%v", a)
	bStr := fmt.Sprintf("%v", b)

	return aStr == bStr
}

// Check whether view data satisfies query conditions.
func CheckViewDataMatchesCondition(viewData map[string]any,
	condition *cond.CondCfg, mappingRules []interfaces.Mapping, isForward bool) bool {

	// Simplified implementation: check view data by mapping rules.
	for _, mapping := range mappingRules {
		var fieldName string
		if isForward {
			fieldName = mapping.TargetProp.Name
		} else {
			fieldName = mapping.SourceProp.Name
		}

		expectedValue, exists := viewData[fieldName]
		if !exists {
			return false
		}

		// Compare values for equality. Relation associations are equality relations, so compare values directly.
		// Read values from conditions; do not take only one value, because multi-field associations must also be considered.
		conditionValue := condition.Value
		if !CompareValues(expectedValue, conditionValue) {
			return false
		}
	}

	return true
}

// Use view data to check indirect mapping conditions.
func CheckIndirectMappingConditionsWithViewData(currentObjectData map[string]any,
	nextObject map[string]any, mappingRules *interfaces.InDirectMapping, isForward bool,
	viewData []map[string]any) bool {

	// Check whether a view record can connect the current object and the next-layer object.
	for _, viewRecord := range viewData {
		// Check the current object -> view record mapping.
		var sourceMapping []interfaces.Mapping
		if isForward {
			sourceMapping = mappingRules.SourceMappingRules
		} else {
			sourceMapping = mappingRules.TargetMappingRules
		}

		sourceMatch := true
		for _, mapping := range sourceMapping {
			sorcePropName := ""
			targetPropName := ""
			if isForward {
				sorcePropName = mapping.SourceProp.Name
				targetPropName = mapping.TargetProp.Name
			} else {
				sorcePropName = mapping.TargetProp.Name
				targetPropName = mapping.SourceProp.Name
			}
			currentValue, ok1 := currentObjectData[sorcePropName]
			viewValue, ok2 := viewRecord[targetPropName]
			if !ok1 || !ok2 || !CompareValues(currentValue, viewValue) {
				sourceMatch = false
				break
			}
		}
		if !sourceMatch {
			continue
		}

		// Check the view record -> next-layer object mapping.
		var targetMapping []interfaces.Mapping
		if isForward {
			targetMapping = mappingRules.TargetMappingRules
		} else {
			targetMapping = mappingRules.SourceMappingRules
		}

		targetMatch := true
		for _, mapping := range targetMapping {
			sorcePropName := ""
			targetPropName := ""
			if isForward {
				sorcePropName = mapping.SourceProp.Name
				targetPropName = mapping.TargetProp.Name
			} else {
				sorcePropName = mapping.TargetProp.Name
				targetPropName = mapping.SourceProp.Name
			}

			viewValue, ok1 := viewRecord[sorcePropName]
			nextValue, ok2 := nextObject[targetPropName]
			if !ok1 || !ok2 || !CompareValues(viewValue, nextValue) {
				targetMatch = false
				break
			}
		}

		if targetMatch {
			return true
		}
	}

	return false
}

// Build object query filter conditions from object unique identities.
func BuildInstanceIdentitiesCondition(uks []map[string]any) *cond.CondCfg {

	if len(uks) == 0 {
		return nil
	}

	ukSubConds := []*cond.CondCfg{}
	for _, uk := range uks { // Multiple objects.
		conds := []*cond.CondCfg{}
		for k, v := range uk { // Composite primary key.
			conds = append(conds, &cond.CondCfg{
				Name:      k,
				Operation: "==",
				ValueOptCfg: cond.ValueOptCfg{
					Value: v,
				},
			})
		}
		ukSubConds = append(ukSubConds, &cond.CondCfg{
			Operation: "and",
			SubConds:  conds,
		})
	}
	var ukCondition *cond.CondCfg
	if len(ukSubConds) > 1 {
		ukCondition = &cond.CondCfg{
			Operation: "or",
			SubConds:  ukSubConds,
		}
	} else {
		ukCondition = ukSubConds[0]
	}
	return ukCondition
}

func TransferPropsToPropMap(props []cond.DataProperty) map[string]*cond.DataProperty {
	propMap := map[string]*cond.DataProperty{}
	for _, prop := range props {
		propMap[prop.Name] = &prop
		// Future plan: if index configuration changes, mark the index status unavailable so queries use virtualization instead of persisted data. Therefore this query result can be considered accurate.
		// if prop.IndexConfig != nil && prop.IndexConfig.FulltextConfig.Enabled {
		// If full-text indexing is configured, define the field type as text.
		// 	propMap[prop.Name] = prop
		// } else if prop.IndexConfig != nil && prop.IndexConfig.VectorConfig.Enabled {
		// Vectorized field.
		// 	propMap[prop.Name] = prop

		// 	propMap[prop.Name] = prop
		// } else {
		// Other cases.
		// 	propMap[prop.Name] = prop
		// }
	}

	return propMap
}

// Build the DSL query.
func BuildDslQuery(ctx context.Context, queryStr string, query *interfaces.ObjectQueryBaseOnObjectType) (map[string]any, error) {

	var dslMap map[string]any
	err := json.Unmarshal([]byte(queryStr), &dslMap)
	if err != nil {
		return map[string]any{}, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
			WithErrorDetails(fmt.Sprintf("failed to unMarshal dslStr to map, %s", err.Error()))
	}

	// Handle sort.
	sort := []map[string]any{}
	for _, sp := range query.Sort {
		// Do not validate sort field parameters here. If the sort field does not exist, OpenSearch will return the error.
		// _score is the field passed to views; because this queries OpenSearch directly, this _score must remain _score.
		// if sp.Field == interfaces.SORT_FIELD_SCORE {
		// 	sort = append(sort, map[string]any{
		// 		"_score": sp.Direction,
		// 	})
		// } else {
		sort = append(sort, map[string]any{
			sp.Field: sp.Direction,
		})
		// }
	}

	if len(query.SearchAfter) > 0 {
		query.NeedTotal = false
	}

	// If limit is omitted but search_after is provided, set the default limit to 10000.
	if query.Limit == 0 && query.SearchAfter != nil && len(query.SearchAfter) > 0 {
		query.Limit = interfaces.SearchAfter_Limit
	}

	dsl := map[string]any{
		"size":         query.Limit,
		"sort":         sort,
		"track_scores": true,
	}
	if len(dslMap) > 0 {
		dsl["query"] = dslMap
	}

	// Add search_after when search after exists.
	if query.SearchAfter != nil {
		dsl["search_after"] = query.SearchAfter
	}

	return dsl, nil
}

// shouldExcludeSystemProperty checks whether the specified system field should be excluded.
func ShouldExcludeSystemProperty(fieldName string, excludeList []string) bool {
	if len(excludeList) == 0 {
		return false
	}
	for _, excludeField := range excludeList {
		if excludeField == fieldName {
			return true
		}
	}
	return false
}

// EvaluateInstanceAgainstCondition evaluates whether instance data satisfies the condition
func EvaluateInstanceAgainstCondition(ctx context.Context,
	instanceData map[string]any,
	condition *cond.CondCfg,
	objectType *interfaces.ObjectType) (bool, error) {

	if condition == nil {
		return true, nil
	}

	// Build property map for quick lookup
	propMap := make(map[string]*cond.DataProperty)
	for i := range objectType.DataProperties {
		propMap[objectType.DataProperties[i].Name] = &objectType.DataProperties[i]
	}

	return evaluateConditionRecursive(ctx, instanceData, condition, propMap)
}

// EvaluateDataAgainstCondition determines whether data satisfies condition.
// data: data to evaluate, with field names as keys.
// Condition: conditionconfiguration.
// paramDefs: parameter definitions for type inference; if nil, fieldType is empty.
func EvaluateDataAgainstCondition(ctx context.Context,
	data map[string]any,
	condition *cond.CondCfg,
	paramDefs []interfaces.Parameter) (bool, error) {

	if condition == nil {
		return true, nil
	}

	propMap := make(map[string]*cond.DataProperty)
	for i := range paramDefs {
		propMap[paramDefs[i].Name] = &cond.DataProperty{
			Name: paramDefs[i].Name,
			Type: paramDefs[i].Type,
		}
	}
	return evaluateConditionRecursive(ctx, data, condition, propMap)
}

// evaluateConditionRecursive recursively evaluates condition
func evaluateConditionRecursive(ctx context.Context,
	instanceData map[string]any,
	condition *cond.CondCfg,
	propMap map[string]*cond.DataProperty) (bool, error) {

	if condition == nil {
		return true, nil
	}

	// Handle logical operators
	switch condition.Operation {
	case cond.OperationAnd:
		for _, subCond := range condition.SubConds {
			result, err := evaluateConditionRecursive(ctx, instanceData, subCond, propMap)
			if err != nil {
				return false, err
			}
			if !result {
				return false, nil
			}
		}
		return true, nil

	case cond.OperationOr:
		for _, subCond := range condition.SubConds {
			result, err := evaluateConditionRecursive(ctx, instanceData, subCond, propMap)
			if err != nil {
				return false, err
			}
			if result {
				return true, nil
			}
		}
		return false, nil
	}

	// Handle field-based operators
	fieldName := condition.Name
	fieldValue, fieldExists := instanceData[fieldName]

	// Handle operators that don't require field existence
	switch condition.Operation {
	case cond.OperationExist:
		return fieldExists, nil
	case cond.OperationNotExist:
		return !fieldExists, nil
	case cond.OperationEmpty:
		if !fieldExists {
			return true, nil
		}
		return isEmpty(fieldValue), nil
	case cond.OperationNotEmpty:
		if !fieldExists {
			return false, nil
		}
		return !isEmpty(fieldValue), nil
	case cond.OperationNull:
		return !fieldExists || fieldValue == nil, nil
	case cond.OperationNotNull:
		return fieldExists && fieldValue != nil, nil
	}

	// For other operators, field must exist (except for != and not_like which can return true if field doesn't exist)
	if !fieldExists {
		if condition.Operation == cond.OperationNotEq || condition.Operation == cond.OperationNotLike {
			return true, nil
		}
		return false, nil
	}

	// Get property info for type checking
	prop, hasProp := propMap[fieldName]
	fieldType := ""
	if hasProp {
		fieldType = prop.Type
	}

	// Evaluate based on operation
	switch condition.Operation {
	case cond.OperationEq:
		return compareValues(fieldValue, condition.Value, "==", fieldType)
	case cond.OperationNotEq:
		return compareValues(fieldValue, condition.Value, "!=", fieldType)
	case cond.OperationGt:
		return compareValues(fieldValue, condition.Value, ">", fieldType)
	case cond.OperationGte:
		return compareValues(fieldValue, condition.Value, ">=", fieldType)
	case cond.OperationLt:
		return compareValues(fieldValue, condition.Value, "<", fieldType)
	case cond.OperationLte:
		return compareValues(fieldValue, condition.Value, "<=", fieldType)
	case cond.OperationIn:
		return evaluateIn(fieldValue, condition.Value)
	case cond.OperationNotIn:
		result, err := evaluateIn(fieldValue, condition.Value)
		if err != nil {
			return false, err
		}
		return !result, nil
	case cond.OperationRange:
		return evaluateRange(fieldValue, condition.Value, fieldType, true)
	case cond.OperationOutRange:
		result, err := evaluateRange(fieldValue, condition.Value, fieldType, true)
		if err != nil {
			return false, err
		}
		return !result, nil
	case cond.OperationTrue:
		return isTrue(fieldValue), nil
	case cond.OperationFalse:
		return isFalse(fieldValue), nil
	case cond.OperationBefore:
		return evaluateBefore(fieldValue, condition.Value)
	case cond.OperationBetween:
		return evaluateRange(fieldValue, condition.Value, fieldType, false)
	case cond.OperationLike:
		pattern, ok := condition.Value.(string)
		if !ok {
			return false, fmt.Errorf("like operation requires string value")
		}
		return evaluateLike(fieldValue, pattern)
	case cond.OperationNotLike:
		pattern, ok := condition.Value.(string)
		if !ok {
			return false, fmt.Errorf("not_like operation requires string value")
		}
		result, err := evaluateLike(fieldValue, pattern)
		if err != nil {
			return false, err
		}
		return !result, nil
	case cond.OperationPrefix:
		prefix, ok := condition.Value.(string)
		if !ok {
			return false, fmt.Errorf("prefix operation requires string value")
		}
		return evaluatePrefix(fieldValue, prefix)
	case cond.OperationNotPrefix:
		prefix, ok := condition.Value.(string)
		if !ok {
			return false, fmt.Errorf("not_prefix operation requires string value")
		}
		result, err := evaluatePrefix(fieldValue, prefix)
		if err != nil {
			return false, err
		}
		return !result, nil
	case cond.OperationRegex:
		pattern, ok := condition.Value.(string)
		if !ok {
			return false, fmt.Errorf("regex operation requires string value")
		}
		return evaluateRegex(fieldValue, pattern)
	case cond.OperationContain:
		return evaluateContain(fieldValue, condition.Value)
	case cond.OperationNotContain:
		result, err := evaluateContain(fieldValue, condition.Value)
		if err != nil {
			return false, err
		}
		return !result, nil
	case cond.OperationCurrent:
		unit, ok := condition.Value.(string)
		if !ok {
			return false, fmt.Errorf("current operation requires string value (year/month/week/day/hour/minute)")
		}
		return evaluateCurrent(fieldValue, unit)
	default:
		return false, fmt.Errorf("unsupported operation: %s", condition.Operation)
	}
}

// isEmpty checks if a value is empty
func isEmpty(value any) bool {
	if value == nil {
		return true
	}

	switch v := value.(type) {
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case []string:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	case map[any]any:
		return len(v) == 0
	}

	// Use reflection for other types
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() == 0
	case reflect.String:
		return rv.Len() == 0
	}

	return false
}

// isTrue checks if value is boolean true
func isTrue(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

// isFalse checks if value is boolean false
func isFalse(value any) bool {
	if b, ok := value.(bool); ok {
		return !b
	}
	return false
}

// evaluateIn checks if fieldValue is in the value list
func evaluateIn(fieldValue any, valueList any) (bool, error) {
	if valueList == nil {
		return false, nil
	}

	list, ok := valueList.([]any)
	if !ok {
		return false, fmt.Errorf("in operation requires array value")
	}

	for _, v := range list {
		if compareValuesSimple(fieldValue, v) {
			return true, nil
		}
	}
	return false, nil
}

// evaluateRange checks if fieldValue is in range [min, max) or [min, max] based on inclusive
func evaluateRange(fieldValue any, rangeValue any, fieldType string, leftClosedRightOpen bool) (bool, error) {
	if rangeValue == nil {
		return false, nil
	}

	rangeList, ok := rangeValue.([]any)
	if !ok || len(rangeList) != 2 {
		return false, fmt.Errorf("range operation requires array of length 2")
	}

	min := rangeList[0]
	max := rangeList[1]

	// Compare fieldValue with min
	minResult, err := compareValues(fieldValue, min, ">=", fieldType)
	if err != nil {
		return false, err
	}
	if !minResult {
		return false, nil
	}

	// Compare fieldValue with max
	if leftClosedRightOpen {
		maxResult, err := compareValues(fieldValue, max, "<", fieldType)
		if err != nil {
			return false, err
		}
		return maxResult, nil
	} else {
		maxResult, err := compareValues(fieldValue, max, "<=", fieldType)
		if err != nil {
			return false, err
		}
		return maxResult, nil
	}
}

// evaluateBefore checks if fieldValue (date) is before conditionValue
func evaluateBefore(fieldValue any, conditionValue any) (bool, error) {
	fieldTime, err := parseTime(fieldValue)
	if err != nil {
		return false, err
	}

	condTime, err := parseTime(conditionValue)
	if err != nil {
		return false, err
	}

	return fieldTime.Before(condTime), nil
}

// evaluateLike checks if fieldValue matches the like pattern (pattern is wrapped as %pattern% for contains semantics)
func evaluateLike(fieldValue any, pattern string) (bool, error) {
	if fieldValue == nil {
		return false, nil
	}
	fieldStr := fmt.Sprintf("%v", fieldValue)
	// Like semantics: %pattern% means contains; use ReplaceLikeWildcards then wrap with .* for full match
	regexPattern := common.ReplaceLikeWildcards(pattern)
	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return false, fmt.Errorf("like pattern invalid: %w", err)
	}
	return re.MatchString(fieldStr), nil
}

// evaluatePrefix checks if fieldValue has the given prefix
func evaluatePrefix(fieldValue any, prefix string) (bool, error) {
	if fieldValue == nil {
		return false, nil
	}
	fieldStr := fmt.Sprintf("%v", fieldValue)
	return strings.HasPrefix(fieldStr, prefix), nil
}

// evaluateRegex checks if fieldValue matches the regex pattern
func evaluateRegex(fieldValue any, pattern string) (bool, error) {
	if fieldValue == nil {
		return false, nil
	}
	fieldStr := fmt.Sprintf("%v", fieldValue)
	re, err := regexp2.Compile(pattern, regexp2.RE2)
	if err != nil {
		return false, fmt.Errorf("regex pattern invalid: %w", err)
	}
	matched, err := re.MatchString(fieldStr)
	if err != nil {
		return false, err
	}
	return matched, nil
}

// evaluateContain checks if fieldValue (array or string) contains target (single value or array)
func evaluateContain(fieldValue any, target any) (bool, error) {
	if fieldValue == nil || target == nil {
		return false, nil
	}

	// Target as array: field must contain all elements
	if targetList, ok := target.([]any); ok {
		if len(targetList) == 0 {
			return false, fmt.Errorf("contain operation requires non-empty array value")
		}
		for _, t := range targetList {
			contains, err := evaluateContainSingle(fieldValue, t)
			if err != nil {
				return false, err
			}
			if !contains {
				return false, nil
			}
		}
		return true, nil
	}

	return evaluateContainSingle(fieldValue, target)
}

// evaluateContainSingle checks if fieldValue contains a single target value
func evaluateContainSingle(fieldValue any, target any) (bool, error) {
	// Field is string: substring contain
	if fieldStr, ok := fieldValue.(string); ok {
		targetStr := fmt.Sprintf("%v", target)
		return strings.Contains(fieldStr, targetStr), nil
	}

	// Field is slice/array: element contain
	rv := reflect.ValueOf(fieldValue)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			elem := rv.Index(i).Interface()
			if compareValuesSimple(elem, target) {
				return true, nil
			}
		}
		return false, nil
	}

	return false, fmt.Errorf("contain operation requires string or array field, got %T", fieldValue)
}

// evaluateCurrent checks if fieldValue (datetime) falls within the current year/month/week/day/hour/minute
func evaluateCurrent(fieldValue any, unit string) (bool, error) {
	fieldTime, err := parseTime(fieldValue)
	if err != nil {
		return false, err
	}

	tz := os.Getenv("TZ")
	if tz == "" {
		tz = "UTC"
	}
	location, err := time.LoadLocation(tz)
	if err != nil {
		location = time.UTC
	}

	now := time.Now().In(location)
	var start, end time.Time

	switch unit {
	case "year":
		start = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, location)
		end = start.AddDate(1, 0, 0)
	case "month":
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location)
		end = start.AddDate(0, 1, 0)
	case "week":
		weekday := now.Weekday()
		offset := int(time.Monday - weekday)
		if offset > 0 {
			offset -= 7
		}
		start = time.Date(now.Year(), now.Month(), now.Day()+offset, 0, 0, 0, 0, location)
		end = start.AddDate(0, 0, 7)
	case "day":
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
		end = start.AddDate(0, 0, 1)
	case "hour":
		start = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, location)
		end = start.Add(time.Hour)
	case "minute":
		start = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), 0, 0, location)
		end = start.Add(time.Minute)
	default:
		return false, fmt.Errorf("current operation requires unit year/month/week/day/hour/minute, got %s", unit)
	}

	// fieldTime must be in [start, end)
	return !fieldTime.Before(start) && fieldTime.Before(end), nil
}

// parseTime parses time from various formats
func parseTime(value any) (time.Time, error) {
	if value == nil {
		return time.Time{}, fmt.Errorf("time value is nil")
	}

	// Try to parse as timestamp (int64 or float64)
	switch v := value.(type) {
	case int64:
		return time.Unix(v, 0), nil
	case float64:
		return time.Unix(int64(v), 0), nil
	case json.Number:
		timestamp, err := v.Int64()
		if err != nil {
			floatTimestamp, floatErr := v.Float64()
			if floatErr != nil {
				return time.Time{}, fmt.Errorf("unable to parse JSON number as time: %s", v)
			}
			timestamp = int64(floatTimestamp)
		}
		return time.Unix(timestamp, 0), nil
	case string:
		// Try parsing as RFC3339
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, nil
		}
		// Try parsing as Unix timestamp string
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.Unix(ts, 0), nil
		}
		return time.Time{}, fmt.Errorf("unable to parse time string: %s", v)
	default:
		return time.Time{}, fmt.Errorf("unsupported time type: %T", value)
	}
}

// compareValues compares two values based on operation
func compareValues(left any, right any, operation string, fieldType string) (bool, error) {
	if left == nil || right == nil {
		return false, nil
	}

	// Handle datetime comparison
	if fieldType == dtype.DATATYPE_DATETIME {
		leftTime, err := parseTime(left)
		if err != nil {
			return false, err
		}
		rightTime, err := parseTime(right)
		if err != nil {
			return false, err
		}

		switch operation {
		case "==":
			return leftTime.Equal(rightTime), nil
		case "!=":
			return !leftTime.Equal(rightTime), nil
		case ">":
			return leftTime.After(rightTime), nil
		case ">=":
			return leftTime.After(rightTime) || leftTime.Equal(rightTime), nil
		case "<":
			return leftTime.Before(rightTime), nil
		case "<=":
			return leftTime.Before(rightTime) || leftTime.Equal(rightTime), nil
		}
	}

	// Handle numeric comparison
	if isNumeric(left) && isNumeric(right) {
		leftNum := toFloat64(left)
		rightNum := toFloat64(right)

		switch operation {
		case "==":
			return leftNum == rightNum, nil
		case "!=":
			return leftNum != rightNum, nil
		case ">":
			return leftNum > rightNum, nil
		case ">=":
			return leftNum >= rightNum, nil
		case "<":
			return leftNum < rightNum, nil
		case "<=":
			return leftNum <= rightNum, nil
		}
	}

	// Handle string comparison
	leftStr := fmt.Sprintf("%v", left)
	rightStr := fmt.Sprintf("%v", right)

	switch operation {
	case "==":
		return leftStr == rightStr, nil
	case "!=":
		return leftStr != rightStr, nil
	case ">":
		return leftStr > rightStr, nil
	case ">=":
		return leftStr >= rightStr, nil
	case "<":
		return leftStr < rightStr, nil
	case "<=":
		return leftStr <= rightStr, nil
	}

	return false, fmt.Errorf("unsupported operation: %s", operation)
}

// compareValuesSimple compares two values for equality (used in in/not_in)
func compareValuesSimple(left any, right any) bool {
	if left == nil && right == nil {
		return true
	}
	if left == nil || right == nil {
		return false
	}

	// Try direct comparison first
	if left == right {
		return true
	}

	// Try numeric comparison
	if isNumeric(left) && isNumeric(right) {
		return toFloat64(left) == toFloat64(right)
	}

	// String comparison
	return fmt.Sprintf("%v", left) == fmt.Sprintf("%v", right)
}

// isNumeric checks if value is numeric
func isNumeric(value any) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64:
		return true
	case uint, uint8, uint16, uint32, uint64:
		return true
	case float32, float64:
		return true
	case json.Number:
		return true
	}
	return false
}

// toFloat64 converts numeric value to float64
func toFloat64(value any) float64 {
	switch v := value.(type) {
	case int:
		return float64(v)
	case int8:
		return float64(v)
	case int16:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case uint:
		return float64(v)
	case uint8:
		return float64(v)
	case uint16:
		return float64(v)
	case uint32:
		return float64(v)
	case uint64:
		return float64(v)
	case float32:
		return float64(v)
	case float64:
		return v
	case json.Number:
		result, _ := v.Float64()
		return result
	}
	return 0
}

// CondCfgToFilterMap serializes a condition tree for vega resource filter_condition JSON.
func CondCfgToFilterMap(c *cond.CondCfg) map[string]any {
	if c == nil {
		return nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// ActionDynamicParamGetValue returns a value from a dynamic-params map using dot-separated keys for nested access.
// Same semantics as action_scheduler.getNestedValue.
func ActionDynamicParamGetValue(data map[string]any, key string) any {
	if data == nil {
		return nil
	}

	if strings.Contains(key, ".") {
		parts := strings.Split(key, ".")
		current := data

		for i, part := range parts {
			if i == len(parts)-1 {
				return current[part]
			}
			next, ok := current[part].(map[string]any)
			if !ok {
				return nil
			}
			current = next
		}
	}

	return data[key]
}

// MissingActionInputDynamicParamNames lists action-type parameter names with value_from=input
// that are missing from dynamicParams (nil map or absent/nil value per ActionDynamicParamGetValue).
func MissingActionInputDynamicParamNames(actionType *interfaces.ActionType, dynamicParams map[string]any) []string {
	var missing []string
	for _, param := range actionType.Parameters {
		if param.ValueFrom != interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT {
			continue
		}
		if ActionDynamicParamGetValue(dynamicParams, param.Name) == nil {
			missing = append(missing, param.Name)
		}
	}
	return missing
}

// FormatMissingParamNames renders a missing-parameter name list as a quoted,
// comma-separated string like `"message", "name"`. Using fmt's default %v on a
// []string yields space-separated names without delimiters (e.g. `[message name]`),
// which reads as a single parameter named "message name" and misleads callers/agents
// (see issue #371). This helper keeps each name individually quoted and comma-separated.
func FormatMissingParamNames(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, fmt.Sprintf("%q", n))
	}
	return strings.Join(quoted, ", ")
}
