// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// PRIMARY_KEY_TYPES are scalar types whose values have a stable document-ID
// representation across the supported table connectors.
var PRIMARY_KEY_TYPES = map[string]struct{}{
	DataType_Integer:         {},
	DataType_UnsignedInteger: {},
	DataType_String:          {},
}

// INCREMENTAL_FIELD_TYPES are scalar types supported by batch keyset sorting,
// cursor filtering, and checkpoint decoding.
var INCREMENTAL_FIELD_TYPES = map[string]struct{}{
	DataType_Integer:         {},
	DataType_UnsignedInteger: {},
	DataType_String:          {},
	DataType_Date:            {},
	DataType_Time:            {},
	DataType_Datetime:        {},
	DataType_Timestamp:       {},
}

func IndexConfig_IsPrimaryKeyType(dataType string) bool {
	_, ok := PRIMARY_KEY_TYPES[dataType]
	return ok
}

func IndexConfig_IsIncrementalFieldType(dataType string) bool {
	_, ok := INCREMENTAL_FIELD_TYPES[dataType]
	return ok
}

// IndexConfigContract contains only configuration that affects local index
// mappings, generated documents, queries, document IDs, or batch cursors.
type IndexConfigContract struct {
	PrimaryKeyFields  []string                   `json:"primary_key_fields"`
	IncrementalFields []string                   `json:"incremental_fields"`
	Fields            []IndexConfigFieldContract `json:"fields"`
}

type IndexConfigFieldContract struct {
	Name         string                       `json:"name"`
	OriginalName string                       `json:"original_name"`
	OriginalType string                       `json:"original_type"`
	Type         string                       `json:"type"`
	Features     []IndexConfigFeatureContract `json:"features"`
	Decimal      *DecimalIndexConfig          `json:"decimal,omitempty"`
}

// DecimalIndexConfig 固化托管索引使用的小数定义，避免源精度变更后复用旧索引。
type DecimalIndexConfig struct {
	Precision int `json:"precision"`
	Scale     int `json:"scale"`
}

type IndexConfigFeatureContract struct {
	Name        string          `json:"name"`
	Type        string          `json:"feature_type"`
	RefProperty string          `json:"ref_property"`
	Config      json.RawMessage `json:"config,omitempty"`
}

// BuildTaskIndexConfig is the index configuration snapshot captured from a
// Resource when a build task is created.
type BuildTaskIndexConfig struct {
	IndexConfigContract
	Features map[string]BuildTaskFieldIndexFeature `json:"features,omitempty"`
}

type BuildTaskFieldIndexFeature struct {
	Vector   *SmallModel              `json:"vector,omitempty"`
	Fulltext *BuildTaskFulltextConfig `json:"fulltext,omitempty"`
}

type BuildTaskFulltextConfig struct {
	Analyzer string `json:"analyzer,omitempty"`
}

// NormalizeIndexKeyValue 校验单个索引键值，并转换为字段对应的 Go 类型。
func NormalizeIndexKeyValue(value any, dataType string) (any, error) {
	if value == nil {
		return nil, fmt.Errorf("key value must not be null")
	}

	switch dataType {
	case DataType_Integer:
		switch number := value.(type) {
		case json.Number:
			parsed, err := strconv.ParseInt(number.String(), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("key value %q is not a valid integer", number.String())
			}
			return parsed, nil
		case int64:
			return number, nil
		default:
			return nil, fmt.Errorf("key value must be a JSON integer, got %T", value)
		}
	case DataType_UnsignedInteger:
		switch number := value.(type) {
		case json.Number:
			parsed, err := strconv.ParseUint(number.String(), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("key value %q is not a valid unsigned integer", number.String())
			}
			return parsed, nil
		case uint64:
			return number, nil
		default:
			return nil, fmt.Errorf("key value must be a JSON unsigned integer, got %T", value)
		}
	case DataType_Date, DataType_Time, DataType_Datetime:
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("key value must be a JSON string, got %T", value)
		}
		// time.Time 经 checkpoint JSON 往返会变成 RFC3339 字符串。在内部边界恢复载体，
		// 供过滤层按源字段解释墙上时间；公共 API 的日期/时间格式要求保持不变。
		if native, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return native, nil
		}
		return text, nil
	case DataType_String, DataType_Timestamp:
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("key value must be a JSON string, got %T", value)
		}
		return text, nil
	default:
		return nil, fmt.Errorf("resource type %q is not supported as a build key", dataType)
	}
}
