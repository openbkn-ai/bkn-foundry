// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "encoding/json"

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
