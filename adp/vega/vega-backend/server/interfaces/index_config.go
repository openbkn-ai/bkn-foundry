// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "encoding/json"

// IndexConfigContract contains only configuration that affects local index
// mappings, generated documents, queries, document IDs, or batch cursors.
type IndexConfigContract struct {
	BuildKeyFields []string                   `json:"build_key_fields"`
	Fields         []IndexConfigFieldContract `json:"fields"`
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
