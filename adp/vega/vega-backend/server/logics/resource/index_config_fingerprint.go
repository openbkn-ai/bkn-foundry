// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"vega-backend/interfaces"
)

// IndexConfigContract contains only Resource configuration that affects local
// index mappings, generated documents, queries, document IDs, or batch cursors.
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
	Type        string          `json:"type"`
	RefProperty string          `json:"ref_property"`
	Config      json.RawMessage `json:"config,omitempty"`
}

// BuildIndexConfigContract normalizes the Resource's effective index
// configuration without reading Catalog, connector, Model Factory, or other
// external state. SourceMetadata and display-only metadata are excluded.
func BuildIndexConfigContract(resource *interfaces.Resource) (IndexConfigContract, error) {
	if resource == nil {
		return IndexConfigContract{}, fmt.Errorf("build index config contract: resource is required")
	}

	contract := IndexConfigContract{
		BuildKeyFields: make([]string, 0),
		Fields:         make([]IndexConfigFieldContract, 0, len(resource.SchemaDefinition)),
	}
	defaultEmbeddingModel := ""
	defaultFulltextAnalyzer := ""
	if resource.IndexConfig != nil {
		contract.BuildKeyFields = append(contract.BuildKeyFields, resource.IndexConfig.BuildKeyFields...)
		defaultEmbeddingModel = strings.TrimSpace(resource.IndexConfig.DefaultEmbeddingModel)
		defaultFulltextAnalyzer = strings.TrimSpace(resource.IndexConfig.DefaultFulltextAnalyzer)
	}

	seenFields := make(map[string]struct{}, len(resource.SchemaDefinition))
	for _, property := range resource.SchemaDefinition {
		if property == nil || property.Name == "" {
			return IndexConfigContract{}, fmt.Errorf("build index config contract: resource schema contains an invalid property")
		}
		if _, exists := seenFields[property.Name]; exists {
			return IndexConfigContract{}, fmt.Errorf("build index config contract: duplicate property %q", property.Name)
		}
		seenFields[property.Name] = struct{}{}

		field := IndexConfigFieldContract{
			Name:         property.Name,
			OriginalName: property.OriginalName,
			OriginalType: property.OriginalType,
			Type:         property.Type,
			Features:     make([]IndexConfigFeatureContract, 0, len(property.Features)),
		}
		seenFeatureTypes := make(map[string]struct{}, len(property.Features))
		for _, feature := range property.Features {
			if feature.FeatureType == "" {
				return IndexConfigContract{}, fmt.Errorf("build index config contract: property %q contains a feature without type", property.Name)
			}
			if _, exists := seenFeatureTypes[feature.FeatureType]; exists {
				return IndexConfigContract{}, fmt.Errorf("build index config contract: property %q has more than one %q feature", property.Name, feature.FeatureType)
			}
			seenFeatureTypes[feature.FeatureType] = struct{}{}

			refProperty := feature.RefProperty
			if refProperty == property.Name {
				refProperty = ""
			}
			config, err := effectiveFeatureConfig(feature, defaultEmbeddingModel, defaultFulltextAnalyzer)
			if err != nil {
				return IndexConfigContract{}, fmt.Errorf("build index config contract for property %q feature %q: %w", property.Name, feature.FeatureType, err)
			}
			field.Features = append(field.Features, IndexConfigFeatureContract{
				Name:        feature.FeatureName,
				Type:        feature.FeatureType,
				RefProperty: refProperty,
				Config:      config,
			})
		}
		sort.Slice(field.Features, func(i, j int) bool {
			left := field.Features[i]
			right := field.Features[j]
			if left.Type != right.Type {
				return left.Type < right.Type
			}
			if left.Name != right.Name {
				return left.Name < right.Name
			}
			if left.RefProperty != right.RefProperty {
				return left.RefProperty < right.RefProperty
			}
			return bytes.Compare(left.Config, right.Config) < 0
		})
		contract.Fields = append(contract.Fields, field)
	}

	sort.Slice(contract.Fields, func(i, j int) bool {
		left := contract.Fields[i]
		right := contract.Fields[j]
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.OriginalName != right.OriginalName {
			return left.OriginalName < right.OriginalName
		}
		if left.Type != right.Type {
			return left.Type < right.Type
		}
		return left.OriginalType < right.OriginalType
	})
	return contract, nil
}

// IndexConfigFingerprint hashes a normalized contract with canonical JSON.
func IndexConfigFingerprint(config IndexConfigContract) (string, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("fingerprint index config: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ResourceIndexConfigFingerprint builds and fingerprints a Resource's current
// effective index configuration.
func ResourceIndexConfigFingerprint(resource *interfaces.Resource) (string, error) {
	contract, err := BuildIndexConfigContract(resource)
	if err != nil {
		return "", err
	}
	return IndexConfigFingerprint(contract)
}

func effectiveFeatureConfig(feature interfaces.PropertyFeature, defaultEmbeddingModel, defaultFulltextAnalyzer string) (json.RawMessage, error) {
	config := make(map[string]any, len(feature.Config)+1)
	for key, value := range feature.Config {
		config[key] = value
	}

	switch feature.FeatureType {
	case interfaces.PropertyFeatureType_Vector:
		modelID := stringConfigValueForFingerprint(config, "embedding_model")
		if modelID == "" {
			modelID = defaultEmbeddingModel
		}
		if modelID == "" {
			delete(config, "embedding_model")
		} else {
			config["embedding_model"] = modelID
		}
	case interfaces.PropertyFeatureType_Fulltext:
		analyzer := stringConfigValueForFingerprint(config, "analyzer")
		if analyzer == "" {
			analyzer = defaultFulltextAnalyzer
		}
		if analyzer == "" {
			delete(config, "analyzer")
		} else {
			config["analyzer"] = analyzer
		}
	}

	if len(config) == 0 {
		return nil, nil
	}
	return canonicalJSON(config)
}

func stringConfigValueForFingerprint(config map[string]any, key string) string {
	value, ok := config[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func canonicalJSON(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not allowed")
		}
		return nil, err
	}
	return json.Marshal(normalized)
}
