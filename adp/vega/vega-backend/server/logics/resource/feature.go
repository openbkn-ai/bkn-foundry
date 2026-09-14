// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See LICENSE-OPENBKN.txt in the project root for details.

package resource

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"vega-backend/interfaces"
)

// IsFeatureSupported reports whether a Property type can generate a Feature type.
func IsFeatureSupported(propertyType string, featureType string) bool {
	switch propertyType {
	case interfaces.DataType_String:
		return featureType == interfaces.PropertyFeatureType_Keyword ||
			featureType == interfaces.PropertyFeatureType_Fulltext ||
			featureType == interfaces.PropertyFeatureType_Vector
	case interfaces.DataType_Text:
		return featureType == interfaces.PropertyFeatureType_Keyword ||
			featureType == interfaces.PropertyFeatureType_Fulltext ||
			featureType == interfaces.PropertyFeatureType_Vector
	case interfaces.DataType_Vector:
		return featureType == interfaces.PropertyFeatureType_Vector
	default:
		return false
	}
}

// NormalizeSelfReferencingFeatures removes ref_property values that point to their own property.
//
// ref_property means that a feature attached to property A acts on field B. Pointing it back to A
// is redundant: capability derivation already falls back to the property itself when ref_property
// is empty (see VegaResourceIndexCaps in bkn-backend), so both forms are equivalent.
//
// The platform historically persisted legacy resources in this form, so normalize them instead of
// rejecting them. Apply normalization to both request and stored schemas; normalizing only one side
// makes their Features unequal, causing validateMutableSchemaUpdate to classify an ordinary edit as
// a build-related change, clear LocalIndexName, and invalidate an existing index.
func NormalizeSelfReferencingFeatures(props []*interfaces.Property) {
	for _, prop := range props {
		if prop == nil {
			continue
		}
		for i := range prop.Features {
			if prop.Features[i].RefProperty == prop.Name {
				prop.Features[i].RefProperty = ""
			}
		}
	}
}

// AddDefaultStringAndTextFeatures 将新提交的本地索引 Schema 补齐为当前特征契约。
// 历史 Schema 在读取和构建时不会通过该函数自动修复。
func AddDefaultStringAndTextFeatures(props []*interfaces.Property, indexConfig *interfaces.ResourceIndexConfig) {
	ignoreAbove := interfaces.DefaultTextKeywordIgnoreAbove
	if indexConfig != nil && indexConfig.DefaultKeywordIgnoreAbove != nil {
		ignoreAbove = *indexConfig.DefaultKeywordIgnoreAbove
	}
	for _, prop := range props {
		if prop.Type != interfaces.DataType_String && prop.Type != interfaces.DataType_Text {
			continue
		}
		if !hasFeature(prop.Features, interfaces.PropertyFeatureType_Keyword) {
			prop.Features = append(prop.Features, interfaces.PropertyFeature{
				FeatureName: interfaces.LocalIndexKeywordSubfieldName,
				FeatureType: interfaces.PropertyFeatureType_Keyword,
				IsDefault:   true,
				Config:      map[string]any{"ignore_above": ignoreAbove},
			})
		} else {
			for i := range prop.Features {
				feature := &prop.Features[i]
				if feature.FeatureType != interfaces.PropertyFeatureType_Keyword {
					continue
				}
				if feature.Config == nil {
					feature.Config = map[string]any{}
				}
				if _, exists := feature.Config["ignore_above"]; !exists {
					feature.Config["ignore_above"] = ignoreAbove
				}
			}
		}
		if prop.Type == interfaces.DataType_Text && !hasFeature(prop.Features, interfaces.PropertyFeatureType_Fulltext) {
			prop.Features = append(prop.Features, interfaces.PropertyFeature{
				FeatureName: interfaces.LocalIndexFulltextSubfieldName,
				FeatureType: interfaces.PropertyFeatureType_Fulltext,
				IsDefault:   true,
			})
		}
	}
}

// FieldsWithoutRequiredDefaultFeatures 返回仍使用旧特征契约的本地索引字段。
func FieldsWithoutRequiredDefaultFeatures(props []*interfaces.Property) []string {
	var fields []string
	for _, prop := range props {
		missing := make([]string, 0, 2)
		if prop.Type == interfaces.DataType_String || prop.Type == interfaces.DataType_Text {
			keywordIndex := -1
			for i := range prop.Features {
				if prop.Features[i].FeatureType == interfaces.PropertyFeatureType_Keyword {
					keywordIndex = i
					break
				}
			}
			if keywordIndex < 0 {
				missing = append(missing, interfaces.PropertyFeatureType_Keyword)
			} else {
				limit, valid := positiveIntegerConfigValue(prop.Features[keywordIndex].Config["ignore_above"])
				if !valid || limit > interfaces.MaxKeywordIgnoreAbove {
					missing = append(missing, "keyword config.ignore_above")
				}
			}
		}
		if prop.Type == interfaces.DataType_Text && !hasFeature(prop.Features, interfaces.PropertyFeatureType_Fulltext) {
			missing = append(missing, interfaces.PropertyFeatureType_Fulltext)
		}
		if len(missing) > 0 {
			fields = append(fields, fmt.Sprintf("%s (%s)", prop.Name, strings.Join(missing, ", ")))
		}
	}
	return fields
}

func hasFeature(features []interfaces.PropertyFeature, featureType string) bool {
	for _, feature := range features {
		if feature.FeatureType == featureType {
			return true
		}
	}
	return false
}

// IsFeatureRefPropertyTypeSupported reports whether a referenced result Property
// has the type produced by a Feature.
func IsFeatureRefPropertyTypeSupported(propertyType string, featureType string) bool {
	switch featureType {
	case interfaces.PropertyFeatureType_Keyword:
		return propertyType == interfaces.DataType_String
	case interfaces.PropertyFeatureType_Fulltext:
		return propertyType == interfaces.DataType_Text
	case interfaces.PropertyFeatureType_Vector:
		return propertyType == interfaces.DataType_Vector
	default:
		return false
	}
}

// ValidateVectorFeatureReferences 校验向量引用只声明目标字段，模型和维度由目标字段自身配置拥有。
func ValidateVectorFeatureReferences(props []*interfaces.Property) error {
	propsByName := make(map[string]*interfaces.Property, len(props))
	for _, prop := range props {
		if prop != nil {
			propsByName[prop.Name] = prop
		}
	}

	for _, prop := range props {
		if prop == nil {
			continue
		}
		for _, feature := range prop.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector || feature.RefProperty == "" {
				continue
			}
			if feature.RefProperty == prop.Name {
				continue
			}

			if len(feature.Config) > 0 {
				return fmt.Errorf("vector feature on field %q that references %q must not define config", prop.Name, feature.RefProperty)
			}

			refProp, exists := propsByName[feature.RefProperty]
			if !exists || refProp.Type != interfaces.DataType_Vector {
				return fmt.Errorf("vector feature on field %q references invalid vector field %q", prop.Name, feature.RefProperty)
			}
			_, ok := ownVectorDimension(refProp)
			if !ok {
				return fmt.Errorf("referenced vector field %q must define its own positive integer dimension", refProp.Name)
			}
		}
	}
	return nil
}

func ownVectorDimension(prop *interfaces.Property) (int64, bool) {
	for _, feature := range prop.Features {
		if feature.FeatureType == interfaces.PropertyFeatureType_Vector {
			return positiveIntegerConfigValue(feature.Config["dimension"])
		}
	}
	return 0, false
}

func positiveIntegerConfigValue(value any) (int64, bool) {
	var dimension int64
	switch value := value.(type) {
	case int:
		dimension = int64(value)
	case int8:
		dimension = int64(value)
	case int16:
		dimension = int64(value)
	case int32:
		dimension = int64(value)
	case int64:
		dimension = value
	case uint:
		if uint64(value) > math.MaxInt64 {
			return 0, false
		}
		dimension = int64(value)
	case uint8:
		dimension = int64(value)
	case uint16:
		dimension = int64(value)
	case uint32:
		dimension = int64(value)
	case uint64:
		if value > math.MaxInt64 {
			return 0, false
		}
		dimension = int64(value)
	case float32:
		floatValue := float64(value)
		if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) || math.Trunc(floatValue) != floatValue || floatValue > math.MaxInt64 {
			return 0, false
		}
		dimension = int64(floatValue)
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value > math.MaxInt64 {
			return 0, false
		}
		dimension = int64(value)
	case json.Number:
		integer, err := value.Int64()
		if err == nil {
			dimension = integer
			break
		}
		floatValue, err := value.Float64()
		if err != nil || math.IsNaN(floatValue) || math.IsInf(floatValue, 0) || math.Trunc(floatValue) != floatValue || floatValue > math.MaxInt64 {
			return 0, false
		}
		dimension = int64(floatValue)
	default:
		return 0, false
	}
	return dimension, dimension > 0
}
