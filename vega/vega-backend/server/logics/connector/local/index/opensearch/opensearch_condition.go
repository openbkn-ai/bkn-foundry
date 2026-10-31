// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package opensearch

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

// 日期查询显式指定纳秒格式，与源字段 mapping 的 format 解耦。
const (
	indexDateQueryFormat         = "strict_date_optional_time_nanos"
	maxIndexTemporalRangeClauses = 1024
	maxIndexTimeTerms            = 65536
)

// ConvertFilterCondition converts a FilterCondition to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterCondition(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {
	query, err := c.convertFilterCondition(condition, schemaDefinition)
	if err != nil {
		return nil, err
	}
	if countIndexRangeClauses(query) > 0 && countIndexQueryClauses(query) > maxIndexTemporalRangeClauses {
		return nil, interfaces.NewConditionBuildError("OpenSearch range filters require at most %d query clauses", maxIndexTemporalRangeClauses)
	}
	return query, nil
}

func (c *OpenSearchConnector) convertFilterCondition(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	switch condition.GetOperation() {
	case filter_condition.OperationAnd:
		return c.ConvertFilterConditionAnd(condition, schemaDefinition)

	case filter_condition.OperationOr:
		return c.ConvertFilterConditionOr(condition, schemaDefinition)

	default:
		return c.ConvertFilterConditionWithOpr(condition, schemaDefinition)
	}
}

// ConvertFilterConditionAnd converts an AndCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionAnd(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	condAnd, ok := condition.(*filter_condition.AndCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.AndCond")
	}

	must := make([]map[string]any, 0, len(condAnd.SubConds))
	for _, subCond := range condAnd.SubConds {
		convertedCond, err := c.ConvertFilterCondition(subCond, schemaDefinition)
		if err != nil {
			return nil, err
		}
		must = append(must, convertedCond)
	}

	return map[string]any{
		"bool": map[string]any{
			"must": must,
		},
	}, nil
}

// ConvertFilterConditionOr converts an OrCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionOr(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	condOr, ok := condition.(*filter_condition.OrCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.OrCond")
	}

	should := make([]map[string]any, 0, len(condOr.SubConds))
	for _, subCond := range condOr.SubConds {
		convertedCond, err := c.ConvertFilterCondition(subCond, schemaDefinition)
		if err != nil {
			return nil, err
		}
		should = append(should, convertedCond)
	}

	return map[string]any{
		"bool": map[string]any{
			"should":               should,
			"minimum_should_match": 1,
		},
	}, nil
}

// ConvertFilterConditionWithOpr converts a FilterCondition with operation to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionWithOpr(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	switch condition.GetOperation() {
	case filter_condition.OperationEqual, filter_condition.OperationEqual2:
		return c.ConvertFilterConditionEqual(condition, schemaDefinition)
	case filter_condition.OperationNotEqual, filter_condition.OperationNotEqual2:
		return c.ConvertFilterConditionNotEqual(condition, schemaDefinition)
	case filter_condition.OperationGt, filter_condition.OperationGt2:
		return c.ConvertFilterConditionGt(condition)
	case filter_condition.OperationGte, filter_condition.OperationGte2:
		return c.ConvertFilterConditionGte(condition)
	case filter_condition.OperationLt, filter_condition.OperationLt2:
		return c.ConvertFilterConditionLt(condition)
	case filter_condition.OperationLte, filter_condition.OperationLte2:
		return c.ConvertFilterConditionLte(condition)
	case filter_condition.OperationIn:
		return c.ConvertFilterConditionIn(condition, schemaDefinition)
	case filter_condition.OperationNotIn:
		return c.ConvertFilterConditionNotIn(condition, schemaDefinition)
	case filter_condition.OperationLike:
		return c.ConvertFilterConditionLike(condition, schemaDefinition)
	case filter_condition.OperationNotLike:
		return c.ConvertFilterConditionNotLike(condition, schemaDefinition)
	case filter_condition.OperationContain:
		return c.ConvertFilterConditionContain(condition, schemaDefinition)
	case filter_condition.OperationNotContain:
		return c.ConvertFilterConditionNotContain(condition, schemaDefinition)
	case filter_condition.OperationRange:
		return c.ConvertFilterConditionRange(condition)
	case filter_condition.OperationOutRange:
		return c.ConvertFilterConditionOutRange(condition)
	case filter_condition.OperationNull:
		return c.ConvertFilterConditionNull(condition)
	case filter_condition.OperationNotNull:
		return c.ConvertFilterConditionNotNull(condition)
	case filter_condition.OperationEmpty:
		return c.ConvertFilterConditionEmpty(condition)
	case filter_condition.OperationNotEmpty:
		return c.ConvertFilterConditionNotEmpty(condition)
	case filter_condition.OperationPrefix:
		return c.ConvertFilterConditionPrefix(condition, schemaDefinition)
	case filter_condition.OperationNotPrefix:
		return c.ConvertFilterConditionNotPrefix(condition, schemaDefinition)
	case filter_condition.OperationBetween:
		return c.ConvertFilterConditionBetween(condition)
	case filter_condition.OperationExist:
		return c.ConvertFilterConditionExist(condition)
	case filter_condition.OperationNotExist:
		return c.ConvertFilterConditionNotExist(condition)
	case filter_condition.OperationRegex:
		return c.ConvertFilterConditionRegex(condition, schemaDefinition)
	case filter_condition.OperationMatch:
		return c.ConvertFilterConditionMatch(condition)
	case filter_condition.OperationMatchPhrase:
		return c.ConvertFilterConditionMatchPhrase(condition)
	case filter_condition.OperationTrue:
		return c.ConvertFilterConditionTrue(condition)
	case filter_condition.OperationFalse:
		return c.ConvertFilterConditionFalse(condition)
	case filter_condition.OperationBefore:
		return c.ConvertFilterConditionBefore(condition)
	case filter_condition.OperationCurrent:
		return c.ConvertFilterConditionCurrent(condition)
	case filter_condition.OperationMultiMatch:
		return c.ConvertFilterConditionMultiMatch(condition)
	case filter_condition.OperationKnnVector:
		return c.ConvertFilterConditionKnnVector(condition, schemaDefinition)
	default:
		return nil, interfaces.NewUnsupportedOperationError(condition.GetOperation(), fmt.Errorf("the opensearch query channel does not support this operation"))
	}
}

// fulltextFieldName returns the physical field name used by match/match_phrase/multi_match.
// A string field uses its configured full-text multi-field, falling back to the default name.
// A text field is already a full-text field and therefore uses its main physical name.
func fulltextFieldName(prop *interfaces.Property) string {
	if prop == nil {
		return ""
	}
	fieldName := propertyPhysicalFieldName(prop)
	if prop.Type == interfaces.DataType_String {
		for _, f := range prop.Features {
			if f.FeatureType == interfaces.PropertyFeatureType_Fulltext {
				return featurePhysicalFieldName(fieldName, f.FeatureName, interfaces.LocalIndexFulltextSubfieldName)
			}
		}
	}
	return fieldName
}

func propertyPhysicalFieldName(prop *interfaces.Property) string {
	if prop == nil {
		return ""
	}
	if prop.OriginalName != "" {
		return prop.OriginalName
	}
	return prop.Name
}

func featurePhysicalFieldName(fieldName string, configuredName string, defaultName string) string {
	featureName := strings.TrimSpace(configuredName)
	if featureName == "" {
		featureName = defaultName
	}
	if strings.HasPrefix(featureName, fieldName+".") {
		return featureName
	}
	return fieldName + "." + featureName
}

// ConvertFilterConditionMultiMatch converts a MultiMatchCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionMultiMatch(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.MultiMatchCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.MultiMatchCond")
	}

	value := cond.Cfg.Value
	fields := make([]string, 0, len(cond.Fields))
	for _, field := range cond.Fields {
		fields = append(fields, fulltextFieldName(field))
	}

	multiMatchQuery := map[string]any{
		"query":  value,
		"fields": fields,
	}

	if cond.MatchType != "" {
		multiMatchQuery["type"] = cond.MatchType
	}

	return map[string]any{
		"multi_match": multiMatchQuery,
	}, nil
}

// ConvertFilterConditionEqual converts an EqualCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionEqual(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.EqualCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.EqualCond")
	}

	fieldName := cond.Lfield.OriginalName
	if fieldName == "" {
		fieldName = cond.Lfield.Name
	}
	exactField, err := c.exactMatchFieldName(fieldName, schemaDefinition)
	if err != nil {
		return nil, err
	}
	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		if err := validateKeywordValues(fieldName, cond.Cfg.Name, cond.Value, schemaDefinition); err != nil {
			return nil, err
		}
		if cond.Lfield.Type == interfaces.DataType_Time {
			query, _, err := indexTemporalSetQuery(exactField, cond.Lfield.Type, []any{cond.Value})
			return query, err
		}
		if _, temporal := cond.Value.(time.Time); temporal {
			query := indexTemporalRangeQuery(exactField, cond.Lfield.Type, map[string]any{
				"gte": cond.Value,
				"lte": cond.Value,
			})
			return query, nil
		}
		return map[string]any{
			"term": map[string]any{
				exactField: indexConditionValue(cond.Value),
			},
		}, nil
	case interfaces.ValueFrom_Field:
		rightFieldName := propertyPhysicalFieldName(cond.Rfield)
		rightExactField, err := c.exactMatchFieldName(rightFieldName, schemaDefinition)
		if err != nil {
			return nil, err
		}
		return fieldComparisonScript(exactField, "==", rightExactField, cond.Lfield.Type, cond.Rfield.Type), nil
	default:
		return nil, interfaces.NewConditionBuildError("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

// ConvertFilterConditionNotEqual converts a NotEqualCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionNotEqual(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.NotEqualCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotEqualCond")
	}

	fieldName := cond.Lfield.OriginalName
	if fieldName == "" {
		fieldName = cond.Lfield.Name
	}
	exactField, err := c.exactMatchFieldName(fieldName, schemaDefinition)
	if err != nil {
		return nil, err
	}
	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		if err := validateKeywordValues(fieldName, cond.Cfg.Name, cond.Value, schemaDefinition); err != nil {
			return nil, err
		}
		if cond.Lfield.Type == interfaces.DataType_Time {
			query, _, err := indexTemporalSetQuery(exactField, cond.Lfield.Type, []any{cond.Value})
			if err != nil {
				return nil, err
			}
			return map[string]any{"bool": map[string]any{"must_not": query}}, nil
		}
		if _, temporal := cond.Value.(time.Time); temporal {
			query := indexTemporalRangeQuery(exactField, cond.Lfield.Type, map[string]any{
				"gte": cond.Value,
				"lte": cond.Value,
			})
			return map[string]any{"bool": map[string]any{"must_not": query}}, nil
		}
		return map[string]any{
			"bool": map[string]any{
				"must_not": map[string]any{
					"term": map[string]any{
						exactField: indexConditionValue(cond.Value),
					},
				},
			},
		}, nil
	case interfaces.ValueFrom_Field:
		rightFieldName := propertyPhysicalFieldName(cond.Rfield)
		rightExactField, err := c.exactMatchFieldName(rightFieldName, schemaDefinition)
		if err != nil {
			return nil, err
		}
		return fieldComparisonScript(exactField, "!=", rightExactField, cond.Lfield.Type, cond.Rfield.Type), nil
	default:
		return nil, interfaces.NewConditionBuildError("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

// ConvertFilterConditionGt converts a GtCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionGt(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.GtCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.GtCond")
	}

	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		return indexTemporalRangeQuery(propertyPhysicalFieldName(cond.Lfield), cond.Lfield.Type, map[string]any{
			"gt": cond.Value,
		}), nil
	case interfaces.ValueFrom_Field:
		return fieldComparisonScript(
			propertyPhysicalFieldName(cond.Lfield), ">", propertyPhysicalFieldName(cond.Rfield), cond.Lfield.Type, cond.Rfield.Type), nil
	default:
		return nil, interfaces.NewConditionBuildError("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

// ConvertFilterConditionGte converts a GteCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionGte(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.GteCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.GteCond")
	}

	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		return indexTemporalRangeQuery(propertyPhysicalFieldName(cond.Lfield), cond.Lfield.Type, map[string]any{
			"gte": cond.Value,
		}), nil
	case interfaces.ValueFrom_Field:
		return fieldComparisonScript(
			propertyPhysicalFieldName(cond.Lfield), ">=", propertyPhysicalFieldName(cond.Rfield), cond.Lfield.Type, cond.Rfield.Type), nil
	default:
		return nil, interfaces.NewConditionBuildError("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

// ConvertFilterConditionLt converts a LtCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionLt(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.LtCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LtCond")
	}

	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		return indexTemporalRangeQuery(propertyPhysicalFieldName(cond.Lfield), cond.Lfield.Type, map[string]any{
			"lt": cond.Value,
		}), nil
	case interfaces.ValueFrom_Field:
		return fieldComparisonScript(
			propertyPhysicalFieldName(cond.Lfield), "<", propertyPhysicalFieldName(cond.Rfield), cond.Lfield.Type, cond.Rfield.Type), nil
	default:
		return nil, interfaces.NewConditionBuildError("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

// ConvertFilterConditionLte converts a LteCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionLte(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.LteCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LteCond")
	}

	switch cond.Cfg.ValueFrom {
	case interfaces.ValueFrom_Const:
		return indexTemporalRangeQuery(propertyPhysicalFieldName(cond.Lfield), cond.Lfield.Type, map[string]any{
			"lte": cond.Value,
		}), nil
	case interfaces.ValueFrom_Field:
		return fieldComparisonScript(
			propertyPhysicalFieldName(cond.Lfield), "<=", propertyPhysicalFieldName(cond.Rfield), cond.Lfield.Type, cond.Rfield.Type), nil
	default:
		return nil, interfaces.NewConditionBuildError("value_from %s is not supported", cond.Cfg.ValueFrom)
	}
}

func fieldComparisonScript(leftField string, operator string, rightField string, leftType string, rightType string) map[string]any {
	// 缺失映射或空值不参与比较，与 SQL WHERE 中 NULL 比较不命中的语义一致。
	guard := "doc.containsKey(params.left) && doc.containsKey(params.right) && " +
		"doc[params.left].size() > 0 && doc[params.right].size() > 0 && "
	comparison := fmt.Sprintf("doc[params.left].value %s doc[params.right].value", operator)
	if (leftType == interfaces.DataType_UnsignedInteger || rightType == interfaces.DataType_UnsignedInteger) &&
		(leftType == interfaces.DataType_Integer || leftType == interfaces.DataType_UnsignedInteger) &&
		(rightType == interfaces.DataType_Integer || rightType == interfaces.DataType_UnsignedInteger) {
		// unsigned_long 的 doc value 是 BigInteger；通用运算符会窄化为 int/long。
		// 两侧整数均转为 BigInteger，避免高位截断，并正确比较有符号和无符号边界。
		comparison = fmt.Sprintf("new BigInteger(doc[params.left].value.toString()).compareTo(new BigInteger(doc[params.right].value.toString())) %s 0", operator)
	} else if leftType == interfaces.DataType_Date || leftType == interfaces.DataType_Datetime {
		// 日期 doc value 是 ZonedDateTime，按 Instant 比较以保留纳秒并按绝对时刻判等。
		comparison = fmt.Sprintf("doc[params.left].value.toInstant().compareTo(doc[params.right].value.toInstant()) %s 0", operator)
	}
	return map[string]any{
		"script": map[string]any{
			"script": map[string]any{
				"source": guard + comparison,
				"params": map[string]any{
					"left":  leftField,
					"right": rightField,
				},
			},
		},
	}
}

// ConvertFilterConditionIn converts an InCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionIn(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.InCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.InCond")
	}

	fieldName := cond.Lfield.OriginalName
	if fieldName == "" {
		fieldName = cond.Lfield.Name
	}
	exactField, err := c.exactMatchFieldName(fieldName, schemaDefinition)
	if err != nil {
		return nil, err
	}
	if err := validateKeywordValues(fieldName, cond.Cfg.Name, cond.Value, schemaDefinition); err != nil {
		return nil, err
	}

	query, temporal, err := indexTemporalSetQuery(exactField, cond.Lfield.Type, cond.Value)
	if err != nil {
		return nil, err
	}
	if temporal {
		return query, nil
	}

	return map[string]any{
		"terms": map[string]any{
			exactField: indexConditionValue(cond.Value),
		},
	}, nil
}

// ConvertFilterConditionNotIn converts a NotInCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionNotIn(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.NotInCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotInCond")
	}

	fieldName := cond.Lfield.OriginalName
	if fieldName == "" {
		fieldName = cond.Lfield.Name
	}
	exactField, err := c.exactMatchFieldName(fieldName, schemaDefinition)
	if err != nil {
		return nil, err
	}
	if err := validateKeywordValues(fieldName, cond.Cfg.Name, cond.Value, schemaDefinition); err != nil {
		return nil, err
	}

	query, temporal, err := indexTemporalSetQuery(exactField, cond.Lfield.Type, cond.Value)
	if err != nil {
		return nil, err
	}
	if temporal {
		return map[string]any{"bool": map[string]any{"must_not": query}}, nil
	}

	return map[string]any{
		"bool": map[string]any{
			"must_not": map[string]any{
				"terms": map[string]any{
					exactField: indexConditionValue(cond.Value),
				},
			},
		},
	}, nil
}

// ConvertFilterConditionLike converts a LikeCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionLike(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.LikeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.LikeCond")
	}

	fieldName := cond.Lfield.OriginalName
	exactField, err := c.exactMatchFieldName(fieldName, schemaDefinition)
	if err != nil {
		return nil, err
	}

	if cond.LegacyWildcards {
		return map[string]any{
			"regexp": map[string]any{
				exactField: c.legacyLikeWildcardRegexp(cond.Value),
			},
		}, nil
	}

	return map[string]any{
		"wildcard": map[string]any{
			exactField: c.likeContainsPattern(cond.Value),
		},
	}, nil
}

// ConvertFilterConditionNotLike converts a NotLikeCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionNotLike(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.NotLikeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotLikeCond")
	}

	fieldName := cond.Lfield.OriginalName
	exactField, err := c.exactMatchFieldName(fieldName, schemaDefinition)
	if err != nil {
		return nil, err
	}

	inner := map[string]any{
		"wildcard": map[string]any{
			exactField: c.likeContainsPattern(cond.Value),
		},
	}
	if cond.LegacyWildcards {
		inner = map[string]any{
			"regexp": map[string]any{
				exactField: c.legacyLikeWildcardRegexp(cond.Value),
			},
		}
	}

	return map[string]any{
		"bool": map[string]any{
			"must_not": inner,
		},
	}, nil
}

// ConvertFilterConditionContain converts a ContainCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionContain(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.ContainCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.ContainCond")
	}

	fieldName := propertyPhysicalFieldName(cond.Lfield)
	exactField, err := c.exactMatchFieldName(fieldName, schemaDefinition)
	if err != nil {
		return nil, err
	}
	if err := validateKeywordValues(fieldName, cond.Cfg.Name, cond.Value, schemaDefinition); err != nil {
		return nil, err
	}

	// 索引集合语义保持任一精确项命中；TEXT 使用 keyword 子字段。
	values := cond.Value
	should := make([]map[string]any, len(values))
	for i, v := range values {
		should[i] = map[string]any{
			"term": map[string]any{
				exactField: v,
			},
		}
	}

	return map[string]any{
		"bool": map[string]any{
			"should":               should,
			"minimum_should_match": 1,
		},
	}, nil
}

// ConvertFilterConditionNotContain converts a NotContainCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionNotContain(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.NotContainCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotContainCond")
	}

	fieldName := propertyPhysicalFieldName(cond.Lfield)
	exactField, err := c.exactMatchFieldName(fieldName, schemaDefinition)
	if err != nil {
		return nil, err
	}
	if err := validateKeywordValues(fieldName, cond.Cfg.Name, cond.Value, schemaDefinition); err != nil {
		return nil, err
	}

	values := cond.Value
	mustNot := make([]map[string]any, len(values))
	for i, v := range values {
		mustNot[i] = map[string]any{
			"term": map[string]any{
				exactField: v,
			},
		}
	}

	return map[string]any{
		"bool": map[string]any{
			"must_not": mustNot,
		},
	}, nil
}

// ConvertFilterConditionRange converts a RangeCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionRange(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.RangeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.RangeCond")
	}

	values := cond.Value
	if len(values) != 2 {
		return nil, interfaces.NewConditionBuildError("range condition requires exactly 2 values")
	}

	return indexTemporalRangeQuery(propertyPhysicalFieldName(cond.Lfield), cond.Lfield.Type, map[string]any{
		"gte": values[0],
		"lte": values[1],
	}), nil
}

// ConvertFilterConditionOutRange converts an OutRangeCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionOutRange(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.OutRangeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.OutRangeCond")
	}

	values := cond.Value
	if len(values) != 2 {
		return nil, interfaces.NewConditionBuildError("out_range condition requires exactly 2 values")
	}

	return map[string]any{
		"bool": map[string]any{
			"should": []map[string]any{
				indexTemporalRangeQuery(propertyPhysicalFieldName(cond.Lfield), cond.Lfield.Type, map[string]any{
					"lt": values[0],
				}),
				indexTemporalRangeQuery(propertyPhysicalFieldName(cond.Lfield), cond.Lfield.Type, map[string]any{
					"gt": values[1],
				}),
			},
			"minimum_should_match": 1,
		},
	}, nil
}

// ConvertFilterConditionNull converts a NullCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionNull(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.NullCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NullCond")
	}

	return map[string]any{
		"bool": map[string]any{
			"must_not": map[string]any{
				"exists": map[string]any{
					"field": cond.Lfield.OriginalName,
				},
			},
		},
	}, nil
}

// ConvertFilterConditionMatch converts a MatchCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionMatch(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.MatchCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.MatchCond")
	}

	value := cond.Cfg.Value

	// If all fields match
	if len(cond.Fields) > 1 {
		should := make([]map[string]any, 0, len(cond.Fields))
		for _, field := range cond.Fields {
			should = append(should, map[string]any{
				"match": map[string]any{
					fulltextFieldName(field): value,
				},
			})
		}
		return map[string]any{
			"bool": map[string]any{
				"should":               should,
				"minimum_should_match": 1,
			},
		}, nil
	} else if len(cond.Fields) == 1 {
		// Single field matching
		field := cond.Fields[0]
		return map[string]any{
			"match": map[string]any{
				fulltextFieldName(field): value,
			},
		}, nil
	}

	return nil, fmt.Errorf("match condition has no fields")
}

// ConvertFilterConditionMatchPhrase converts a MatchPhraseCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionMatchPhrase(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.MatchPhraseCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.MatchPhraseCond")
	}

	value := cond.Cfg.Value

	// If all fields match
	if len(cond.Fields) > 1 {
		should := make([]map[string]any, 0, len(cond.Fields))
		for _, field := range cond.Fields {
			should = append(should, map[string]any{
				"match_phrase": map[string]any{
					fulltextFieldName(field): value,
				},
			})
		}
		return map[string]any{
			"bool": map[string]any{
				"should":               should,
				"minimum_should_match": 1,
			},
		}, nil
	} else if len(cond.Fields) == 1 {
		// Single field matching
		field := cond.Fields[0]
		return map[string]any{
			"match_phrase": map[string]any{
				fulltextFieldName(field): value,
			},
		}, nil
	}

	return nil, fmt.Errorf("match_phrase condition has no fields")
}

// ConvertFilterConditionNotNull converts a NotNullCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionNotNull(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.NotNullCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotNullCond")
	}

	return map[string]any{
		"exists": map[string]any{
			"field": cond.Lfield.OriginalName,
		},
	}, nil
}

// ConvertFilterConditionEmpty converts an EmptyCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionEmpty(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.EmptyCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.EmptyCond")
	}

	return map[string]any{
		"bool": map[string]any{
			"should": []map[string]any{
				{
					"term": map[string]any{
						cond.Lfield.OriginalName: "",
					},
				},
				{
					"bool": map[string]any{
						"must_not": map[string]any{
							"exists": map[string]any{
								"field": cond.Lfield.OriginalName,
							},
						},
					},
				},
			},
			"minimum_should_match": 1,
		},
	}, nil
}

// ConvertFilterConditionNotEmpty converts a NotEmptyCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionNotEmpty(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.NotEmptyCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotEmptyCond")
	}

	return map[string]any{
		"bool": map[string]any{
			"must": []map[string]any{
				{
					"exists": map[string]any{
						"field": cond.Lfield.OriginalName,
					},
				},
				{
					"bool": map[string]any{
						"must_not": map[string]any{
							"term": map[string]any{
								cond.Lfield.OriginalName: "",
							},
						},
					},
				},
			},
		},
	}, nil
}

// ConvertFilterConditionPrefix converts a PrefixCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionPrefix(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.PrefixCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.PrefixCond")
	}

	fieldName := propertyPhysicalFieldName(cond.Lfield)
	exactField, err := c.exactMatchFieldName(fieldName, schemaDefinition)
	if err != nil {
		return nil, err
	}

	vStr := cond.Value
	return map[string]any{
		"prefix": map[string]any{
			exactField: vStr,
		},
	}, nil
}

// ConvertFilterConditionNotPrefix converts a NotPrefixCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionNotPrefix(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.NotPrefixCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotPrefixCond")
	}

	fieldName := propertyPhysicalFieldName(cond.Lfield)
	exactField, err := c.exactMatchFieldName(fieldName, schemaDefinition)
	if err != nil {
		return nil, err
	}

	vStr := cond.Value
	return map[string]any{
		"bool": map[string]any{
			"must_not": map[string]any{
				"prefix": map[string]any{
					exactField: vStr,
				},
			},
		},
	}, nil
}

// ConvertFilterConditionBetween converts a BetweenCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionBetween(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.BetweenCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.BetweenCond")
	}

	values := cond.Value
	if len(values) != 2 {
		return nil, interfaces.NewConditionBuildError("between condition requires exactly 2 values")
	}

	return indexTemporalRangeQuery(propertyPhysicalFieldName(cond.Lfield), cond.Lfield.Type, map[string]any{
		"gte": values[0],
		"lte": values[1],
	}), nil
}

// ConvertFilterConditionExist converts an ExistCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionExist(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.ExistCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.ExistCond")
	}

	return map[string]any{
		"exists": map[string]any{
			"field": cond.Lfield.OriginalName,
		},
	}, nil
}

// ConvertFilterConditionNotExist converts a NotExistCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionNotExist(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.NotExistCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.NotExistCond")
	}

	return map[string]any{
		"bool": map[string]any{
			"must_not": map[string]any{
				"exists": map[string]any{
					"field": cond.Lfield.OriginalName,
				},
			},
		},
	}, nil
}

// ConvertFilterConditionRegex converts a RegexCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionRegex(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.RegexCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.RegexCond")
	}

	fieldName := propertyPhysicalFieldName(cond.Lfield)
	exactField, err := c.exactMatchFieldName(fieldName, schemaDefinition)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"regexp": map[string]any{
			exactField: cond.Value,
		},
	}, nil
}

// ConvertFilterConditionTrue converts a TrueCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionTrue(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.TrueCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.TrueCond")
	}
	if cond.Lfield.Type != interfaces.DataType_Boolean {
		return nil, fmt.Errorf("opensearch true condition requires BOOLEAN: %s", cond.Lfield.Name)
	}

	return map[string]any{
		"term": map[string]any{
			cond.Lfield.OriginalName: true,
		},
	}, nil
}

// ConvertFilterConditionFalse converts a FalseCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionFalse(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.FalseCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.FalseCond")
	}
	if cond.Lfield.Type != interfaces.DataType_Boolean {
		return nil, fmt.Errorf("opensearch false condition requires BOOLEAN: %s", cond.Lfield.Name)
	}

	return map[string]any{
		"term": map[string]any{
			cond.Lfield.OriginalName: false,
		},
	}, nil
}

// ConvertFilterConditionBefore converts a BeforeCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionBefore(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.BeforeCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.BeforeCond")
	}
	return indexBeforeQuery(cond, time.Now())
}

// indexBeforeQuery 显式接收查询时刻，使相对时间转换可用固定时钟验证。
func indexBeforeQuery(cond *filter_condition.BeforeCond, now time.Time) (map[string]any, error) {
	if len(cond.Value) != 2 {
		return nil, interfaces.NewConditionBuildError("before condition requires exactly 2 values")
	}

	unit, ok := cond.Value[1].(string)
	if !ok {
		return nil, interfaces.NewConditionBuildError("before unit must be a string")
	}
	fieldName := propertyPhysicalFieldName(cond.Lfield)
	// 兼容既有索引定义的 [小时数, RFC3339 参考时刻]，保持其原有查询边界。
	if reference, err := time.Parse(time.RFC3339, unit); err == nil {
		interval, ok := common.NumberAsFloat64(cond.Value[0])
		if !ok {
			return nil, interfaces.NewConditionBuildError("before interval must be a number")
		}
		cutoff := reference.Add(-time.Duration(interval) * time.Hour)
		return map[string]any{"range": map[string]any{fieldName: map[string]any{"lt": cutoff.Format(time.RFC3339)}}}, nil
	}
	interval, ok := common.NumberAsInt64(cond.Value[0])
	// OpenSearch 的 date math 数量按 Java int 解析。
	if !ok || interval <= 0 || interval > math.MaxInt32 {
		return nil, interfaces.NewConditionBuildError("before interval must be a positive integer no greater than %d", math.MaxInt32)
	}
	mathUnit := ""
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "year":
		mathUnit = "y"
	case "month":
		mathUnit = "M"
	case "week":
		mathUnit = "w"
	case "day":
		mathUnit = "d"
	case "hour":
		mathUnit = "h"
	case "minute":
		mathUnit = "m"
	case "second":
		mathUnit = "s"
	default:
		return nil, interfaces.NewConditionBuildError("unsupported before unit: %s", unit)
	}
	if cond.Lfield.Type != interfaces.DataType_Date && cond.Lfield.Type != interfaces.DataType_Datetime {
		return nil, interfaces.NewConditionBuildError("index before requires a date or datetime field")
	}
	zone := common.ServerTimeZone()
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, interfaces.NewServerTimeZoneError()
	}
	anchor := now.UTC()
	if cond.Lfield.Type == interfaces.DataType_Date {
		// date 按日历日期索引为 UTC 午夜；在相同墙钟坐标比较，避免服务时区换日偏移。
		wall := now.In(location)
		anchor = time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), time.UTC)
		zone = "UTC"
	}
	// 由引擎完成月末截断、闰年和夏令时的日历计算，锚点保留纳秒精度。
	bound := fmt.Sprintf("%s||-%d%s", anchor.Format(common.RFC3339FixedNanosLayout), interval, mathUnit)
	return map[string]any{
		"range": map[string]any{
			fieldName: map[string]any{
				"lt":        bound,
				"format":    indexDateQueryFormat,
				"time_zone": zone,
			},
		},
	}, nil
}

// ConvertFilterConditionCurrent converts a CurrentCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionCurrent(condition interfaces.FilterCondition) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.CurrentCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.CurrentCond")
	}

	// Get current time
	now := time.Now()
	// Calculate the start and end of the current period
	var startTime, endTime time.Time
	switch cond.Value {
	case filter_condition.CurrentYear:
		startTime = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
		endTime = startTime.AddDate(1, 0, 0)
	case filter_condition.CurrentMonth:
		startTime = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		endTime = startTime.AddDate(0, 1, 0)
	case filter_condition.CurrentWeek:
		// Get Monday of the current week
		weekday := now.Weekday()
		if weekday == time.Sunday {
			weekday = 7
		}
		startTime = time.Date(now.Year(), now.Month(), now.Day()-int(weekday)+1, 0, 0, 0, 0, now.Location())
		endTime = startTime.AddDate(0, 0, 7)
	case filter_condition.CurrentDay:
		startTime = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		endTime = startTime.AddDate(0, 0, 1)
	case filter_condition.CurrentHour:
		startTime = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
		endTime = startTime.Add(time.Hour)
	case filter_condition.CurrentMinute:
		startTime = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), 0, 0, now.Location())
		endTime = startTime.Add(time.Minute)
	}

	return map[string]any{
		"range": map[string]any{
			cond.Lfield.OriginalName: map[string]any{
				"gte": startTime.Format(time.RFC3339),
				"lt":  endTime.Format(time.RFC3339),
			},
		},
	}, nil
}

// ConvertFilterConditionKnnVector converts a KnnVectorCond to OpenSearch DSL.
func (c *OpenSearchConnector) ConvertFilterConditionKnnVector(condition interfaces.FilterCondition, schemaDefinition []*interfaces.Property) (map[string]any, error) {

	cond, ok := condition.(*filter_condition.KnnVectorCond)
	if !ok {
		return nil, fmt.Errorf("condition is not *filter_condition.KnnVectorCond")
	}

	value := cond.Cfg.Value

	// Build a knn query
	knnQuery := map[string]any{
		cond.FilterFieldName: map[string]any{
			"vector": value,
		},
	}

	// Add limit_key and limit_value. A direct k is also accepted for compatibility
	// with the OpenSearch DSL and takes effect only when the generic limit config is absent.
	if limitKey, ok := cond.Cfg.RemainCfg["limit_key"].(string); ok && limitKey != "" {
		if limitValue, ok := cond.Cfg.RemainCfg["limit_value"]; ok {
			knnQuery[cond.FilterFieldName].(map[string]any)[limitKey] = limitValue
		}
	} else if k, ok := cond.Cfg.RemainCfg["k"]; ok {
		knnQuery[cond.FilterFieldName].(map[string]any)["k"] = k
	} else {
		// Use the default value
		knnQuery[cond.FilterFieldName].(map[string]any)["k"] = 10
	}

	// Add sub-conditions
	if len(cond.SubConds) > 0 {
		filterQueries := make([]map[string]any, 0, len(cond.SubConds))
		for _, subCond := range cond.SubConds {
			subQuery, err := c.ConvertFilterCondition(subCond, schemaDefinition)
			if err != nil {
				return nil, err
			}
			filterQueries = append(filterQueries, subQuery)
		}

		knnQuery[cond.FilterFieldName].(map[string]any)["filter"] = map[string]any{
			"bool": map[string]any{
				"must": filterQueries,
			},
		}
	}

	return map[string]any{
		"knn": knnQuery,
	}, nil
}

// likeContainsPattern converts a literal like/not_like substring into an OpenSearch wildcard pattern.
//
// The like contract uses substring containment. ParseLikeValue has already handled % and _,
// so the value is matched literally here; escape the wildcard metacharacters * ? \ and wrap it in *.
func (c *OpenSearchConnector) likeContainsPattern(input string) string {
	var escaped strings.Builder
	for _, r := range input {
		if r == '*' || r == '?' || r == '\\' {
			escaped.WriteRune('\\')
		}
		escaped.WriteRune(r)
	}
	return "*" + escaped.String() + "*"
}

// legacyLikeWildcardRegexp reproduces how the index path handled a like value before this
// change: % -> .*, _ -> ., backslash escapes preserved. It is used only for conditions marked
// as legacy (see filter_condition.MarkLegacyLikeWildcards), so stored view definitions return
// the same rows as before; new values go through likeContainsPattern's literal semantics.
func (c *OpenSearchConnector) legacyLikeWildcardRegexp(input string) string {
	if input == "" {
		return input
	}

	var result strings.Builder
	escaped := false
	runes := []rune(input)

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if escaped {
			switch r {
			case '%', '_', '\\':
				result.WriteRune(r)
			default:
				result.WriteRune('\\')
				result.WriteRune(r)
			}
			escaped = false
		} else if r == '\\' {
			if i == len(runes)-1 {
				result.WriteRune(r)
			} else {
				escaped = true
			}
		} else if r == '%' {
			result.WriteString(".*")
		} else if r == '_' {
			result.WriteString(".")
		} else {
			result.WriteRune(r)
		}
	}

	if escaped {
		result.WriteRune('\\')
	}

	return result.String()
}

// exactMatchProperty 为别名查找目标字段，复用目标的 keyword 配置和校验元数据。
// 别名不能指向另一个别名；目标不在 schema 中时保留原字段，由调用方保守校验。
func exactMatchProperty(fieldName string, schemaDefinition []*interfaces.Property) *interfaces.Property {
	for _, prop := range schemaDefinition {
		if prop == nil || propertyPhysicalFieldName(prop) != fieldName {
			continue
		}
		if prop.OriginalType == "alias" {
			path, _ := prop.Attributes["path"].(string)
			for _, target := range schemaDefinition {
				if target != nil && target.OriginalType != "alias" && propertyPhysicalFieldName(target) == path {
					return target
				}
			}
		}
		return prop
	}
	return nil
}

// exactMatchFieldName 将 text 字段或其别名路由到目标字段的 keyword 子字段。
func (c *OpenSearchConnector) exactMatchFieldName(fieldName string, schemaDefinition []*interfaces.Property) (string, error) {
	prop := exactMatchProperty(fieldName, schemaDefinition)
	if prop == nil {
		return "", interfaces.NewConditionBuildError("field %q not found in schema", fieldName)
	}
	physicalName := propertyPhysicalFieldName(prop)
	if prop.Type != interfaces.DataType_Text {
		return physicalName, nil
	}
	for _, feature := range prop.Features {
		if feature.FeatureType == interfaces.PropertyFeatureType_Keyword {
			return featurePhysicalFieldName(physicalName, feature.FeatureName, interfaces.LocalIndexKeywordSubfieldName), nil
		}
	}
	return "", interfaces.NewConditionBuildError("text field %s has no keyword feature; re-save the resource configuration and rebuild the local index, or use match", fieldName)
}

// validateKeywordValues 使用物理字段配置校验，超限错误保留调用方请求的字段名。
func validateKeywordValues(physicalFieldName, requestFieldName string, value any, schemaDefinition []*interfaces.Property) error {
	prop := exactMatchProperty(physicalFieldName, schemaDefinition)
	if prop == nil || (prop.Type != interfaces.DataType_String && prop.Type != interfaces.DataType_Text) {
		return nil
	}
	for _, feature := range prop.Features {
		if feature.FeatureType != interfaces.PropertyFeatureType_Keyword {
			continue
		}
		limit, ok := positiveInt(feature.Config["ignore_above"])
		if !ok {
			return nil
		}
		values, ok := value.([]any)
		if !ok {
			values = []any{value}
		}
		for _, candidate := range values {
			text, ok := candidate.(string)
			// OpenSearch compares ignore_above with Java String.length(), whose
			// unit is UTF-16 code units rather than Unicode code points.
			if ok && len(utf16.Encode([]rune(text))) > limit {
				return interfaces.NewKeywordLimitError(requestFieldName, limit)
			}
		}
		return nil
	}
	return nil
}

func positiveInt(value any) (int, bool) {
	typed, ok := common.NumberAsInt64(value)
	converted := int(typed)
	if !ok || typed != int64(converted) || converted <= 0 {
		return 0, false
	}
	return converted, true
}

// indexConditionValue 将公共层的 datetime 时刻编码为明确的 UTC RFC3339 文本，
// date/time 字符串保持日历语义，集合值逐项处理，不改写条件本身。
func indexConditionValue(value any) any {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC().Format(common.RFC3339FixedNanosLayout)
	case []any:
		values := make([]any, len(typed))
		for i, item := range typed {
			values[i] = indexConditionValue(item)
		}
		return values
	default:
		return value
	}
}

func indexTemporalRangeQuery(field, fieldType string, bounds map[string]any) map[string]any {
	encoded := make(map[string]any, len(bounds)+1)
	for op, value := range bounds {
		encoded[op] = indexConditionValue(value)
		if fieldType == interfaces.DataType_Time {
			// 对合法 HH:mm:ss[.fraction]，同一时刻的所有尾零形式位于这两个字典序边界之间。
			if text, ok := value.(string); ok {
				if clock, err := time.Parse(common.TimeLayout, text); err == nil {
					layout := common.TimeLayout
					if op == "gt" || op == "lte" {
						layout = common.TimeFixedNanosLayout
					}
					encoded[op] = clock.Format(layout)
				}
			}
		}
		if _, temporal := value.(time.Time); temporal {
			encoded["format"] = indexDateQueryFormat
		}
	}
	return map[string]any{"range": map[string]any{field: encoded}}
}

// 日期 terms 在引擎内仍展开为布尔子句，且整数毫秒会扩大 date_nanos 的匹配范围。
// datetime 使用固定纳秒精度的闭区间；time keyword 使用有限的等价尾零集合。
func indexTemporalSetQuery(field, fieldType string, values []any) (map[string]any, bool, error) {
	if len(values) == 0 {
		return nil, false, nil
	}
	unique := make([]any, 0, len(values))
	seen := make(map[any]struct{}, len(values))
	for _, value := range values {
		if fieldType == interfaces.DataType_Time {
			parsed, err := filter_condition.ParseTemporalValue(fieldType, value, common.ServerTimeZone())
			if err != nil {
				return nil, true, err
			}
			value = parsed
		} else if instant, temporal := value.(time.Time); temporal {
			value = instant.UTC()
		} else {
			return nil, false, nil
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	if fieldType == interfaces.DataType_Time {
		// keyword terms 不会像日期 terms 一样展开布尔子句；枚举最多 10 种等价尾零形式。
		if len(unique) > maxIndexTimeTerms {
			return nil, true, interfaces.NewConditionBuildError("OpenSearch time sets support at most %d expanded terms", maxIndexTimeTerms)
		}
		terms := make([]any, 0, len(unique))
		for _, value := range unique {
			text := value.(string)
			terms = append(terms, text)
			if !strings.Contains(text, ".") {
				text += "."
			}
			for len(text) < len(common.TimeFixedNanosLayout) {
				text += "0"
				terms = append(terms, text)
			}
			if len(terms) > maxIndexTimeTerms {
				return nil, true, interfaces.NewConditionBuildError("OpenSearch time sets support at most %d expanded terms", maxIndexTimeTerms)
			}
		}
		return map[string]any{"terms": map[string]any{field: terms}}, true, nil
	}
	if len(unique) > maxIndexTemporalRangeClauses {
		return nil, true, interfaces.NewConditionBuildError("OpenSearch temporal sets support at most %d distinct values", maxIndexTemporalRangeClauses)
	}
	queries := make([]map[string]any, len(unique))
	for i, value := range unique {
		queries[i] = indexTemporalRangeQuery(field, fieldType, map[string]any{"gte": value, "lte": value})
	}
	return map[string]any{"bool": map[string]any{"should": queries, "minimum_should_match": 1}}, true, nil
}

// 按整个条件树计算 range 预算，覆盖 datetime/time 集合及混合条件。
func countIndexRangeClauses(value any) int {
	count := 0
	switch typed := value.(type) {
	case map[string]any:
		if ranges, ok := typed["range"].(map[string]any); ok {
			count += len(ranges)
		}
		for _, child := range typed {
			count += countIndexRangeClauses(child)
		}
	case []map[string]any:
		for _, child := range typed {
			count += countIndexRangeClauses(child)
		}
	case []any:
		for _, child := range typed {
			count += countIndexRangeClauses(child)
		}
	}
	return count
}

func countIndexQueryClauses(value any) int {
	switch query := value.(type) {
	case map[string]any:
		if boolean, ok := query["bool"].(map[string]any); ok {
			count := 0
			for _, op := range []string{"must", "filter", "should", "must_not"} {
				count += countIndexQueryClauses(boolean[op])
			}
			return count
		}
		return 1
	case []map[string]any:
		count := 0
		for _, query := range query {
			count += countIndexQueryClauses(query)
		}
		return count
	case []any:
		count := 0
		for _, query := range query {
			count += countIndexQueryClauses(query)
		}
		return count
	}
	return 0
}
