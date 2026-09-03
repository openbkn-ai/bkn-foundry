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

	"github.com/bytedance/sonic"

	"vega-backend/interfaces"
)

// BuildIndexConfigContract normalizes the Resource's effective index
// configuration without reading Catalog, connector, Model Factory, or other
// external state. SourceMetadata and display-only metadata are excluded.
func BuildIndexConfigContract(resource *interfaces.Resource) (interfaces.IndexConfigContract, error) {
	if resource == nil {
		return interfaces.IndexConfigContract{}, fmt.Errorf("build index config contract: resource is required")
	}

	contract := interfaces.IndexConfigContract{
		PrimaryKeyFields:  make([]string, 0),
		IncrementalFields: make([]string, 0),
		Fields:            make([]interfaces.IndexConfigFieldContract, 0, len(resource.SchemaDefinition)),
	}
	defaultEmbeddingModel := ""
	defaultFulltextAnalyzer := ""
	if resource.IndexConfig != nil {
		contract.PrimaryKeyFields = append(contract.PrimaryKeyFields, resource.IndexConfig.PrimaryKeyFields...)
		contract.IncrementalFields = append(contract.IncrementalFields, resource.IndexConfig.IncrementalFields...)
		defaultEmbeddingModel = strings.TrimSpace(resource.IndexConfig.DefaultEmbeddingModel)
		defaultFulltextAnalyzer = strings.TrimSpace(resource.IndexConfig.DefaultFulltextAnalyzer)
	}

	seenFields := make(map[string]struct{}, len(resource.SchemaDefinition))
	for _, property := range resource.SchemaDefinition {
		if property == nil || property.Name == "" {
			return interfaces.IndexConfigContract{}, fmt.Errorf("build index config contract: resource schema contains an invalid property")
		}
		if _, exists := seenFields[property.Name]; exists {
			return interfaces.IndexConfigContract{}, fmt.Errorf("build index config contract: duplicate property %q", property.Name)
		}
		seenFields[property.Name] = struct{}{}

		field := interfaces.IndexConfigFieldContract{
			Name:         property.Name,
			OriginalName: property.OriginalName,
			OriginalType: property.OriginalType,
			Type:         property.Type,
			Features:     make([]interfaces.IndexConfigFeatureContract, 0, len(property.Features)),
		}
		seenFeatureTypes := make(map[string]struct{}, len(property.Features))
		for _, feature := range property.Features {
			if feature.FeatureType == "" {
				return interfaces.IndexConfigContract{}, fmt.Errorf("build index config contract: property %q contains a feature without type", property.Name)
			}
			if _, exists := seenFeatureTypes[feature.FeatureType]; exists {
				return interfaces.IndexConfigContract{}, fmt.Errorf("build index config contract: property %q has more than one %q feature", property.Name, feature.FeatureType)
			}
			seenFeatureTypes[feature.FeatureType] = struct{}{}

			refProperty := feature.RefProperty
			if refProperty == property.Name {
				refProperty = ""
			}
			config, err := effectiveFeatureConfig(feature, defaultEmbeddingModel, defaultFulltextAnalyzer)
			if err != nil {
				return interfaces.IndexConfigContract{}, fmt.Errorf("build index config contract for property %q feature %q: %w", property.Name, feature.FeatureType, err)
			}
			field.Features = append(field.Features, interfaces.IndexConfigFeatureContract{
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

	return normalizeIndexConfigContract(contract)
}

// BuildTaskIndexConfigFingerprint calculates the fingerprint from the index
// configuration snapshot persisted on a build task.
func BuildTaskIndexConfigFingerprint(config *interfaces.BuildTaskIndexConfig) (string, error) {
	contract, err := BuildTaskIndexConfigContract(config)
	if err != nil {
		return "", err
	}
	return IndexConfigFingerprint(contract)
}

// BuildTaskIndexConfigContract builds the effective index configuration from a
// task snapshot. Resolved task features supply the effective vector model and
// fulltext analyzer.
func BuildTaskIndexConfigContract(config *interfaces.BuildTaskIndexConfig) (interfaces.IndexConfigContract, error) {
	if config == nil {
		return interfaces.IndexConfigContract{}, fmt.Errorf("build task index config is required")
	}

	contract := cloneIndexConfigContract(config.IndexConfigContract)
	for fieldIndex := range contract.Fields {
		field := &contract.Fields[fieldIndex]
		for featureIndex := range field.Features {
			feature := &field.Features[featureIndex]
			fieldName := field.Name
			if feature.RefProperty != "" {
				fieldName = feature.RefProperty
			}
			effectiveConfig, err := effectiveBuildTaskFeatureConfig(*feature, config.Features[fieldName])
			if err != nil {
				return interfaces.IndexConfigContract{}, fmt.Errorf("build task index config snapshot field %q feature %q: %w", field.Name, feature.Type, err)
			}
			feature.Config = effectiveConfig
		}
	}

	normalized, err := normalizeIndexConfigContract(contract)
	if err != nil {
		return interfaces.IndexConfigContract{}, fmt.Errorf("build task index config snapshot: %w", err)
	}
	return normalized, nil
}

func effectiveBuildTaskFeatureConfig(feature interfaces.IndexConfigFeatureContract, snapshot interfaces.BuildTaskFieldIndexFeature) (json.RawMessage, error) {
	config := map[string]any{}
	if len(feature.Config) > 0 && string(feature.Config) != "null" {
		decoder := json.NewDecoder(bytes.NewReader(feature.Config))
		decoder.UseNumber()
		if err := decoder.Decode(&config); err != nil {
			return nil, err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			if err == nil {
				return nil, fmt.Errorf("multiple JSON values are not allowed")
			}
			return nil, err
		}
	}

	switch feature.Type {
	case interfaces.PropertyFeatureType_Vector:
		if snapshot.Vector != nil {
			config["embedding_model"] = snapshot.Vector.ModelID
		}
	case interfaces.PropertyFeatureType_Fulltext:
		if snapshot.Fulltext != nil {
			config["analyzer"] = snapshot.Fulltext.Analyzer
		}
	}
	return effectiveFeatureConfig(interfaces.PropertyFeature{FeatureType: feature.Type, Config: config}, "", "")
}

// SnapshotBuildTaskIndexConfigFields copies normalized index configuration
// contract fields into the build task configuration snapshot.
func SnapshotBuildTaskIndexConfigFields(resource *interfaces.Resource) ([]interfaces.IndexConfigFieldContract, error) {
	contract, err := BuildIndexConfigContract(resource)
	if err != nil {
		return nil, err
	}

	return cloneIndexConfigContract(contract).Fields, nil
}

func cloneIndexConfigContract(contract interfaces.IndexConfigContract) interfaces.IndexConfigContract {
	cloned := interfaces.IndexConfigContract{
		PrimaryKeyFields:  append([]string(nil), contract.PrimaryKeyFields...),
		IncrementalFields: append([]string(nil), contract.IncrementalFields...),
		Fields:            make([]interfaces.IndexConfigFieldContract, 0, len(contract.Fields)),
	}
	for _, field := range contract.Fields {
		clonedField := interfaces.IndexConfigFieldContract{
			Name:         field.Name,
			OriginalName: field.OriginalName,
			OriginalType: field.OriginalType,
			Type:         field.Type,
			Features:     make([]interfaces.IndexConfigFeatureContract, 0, len(field.Features)),
		}
		for _, feature := range field.Features {
			clonedField.Features = append(clonedField.Features, interfaces.IndexConfigFeatureContract{
				Name:        feature.Name,
				Type:        feature.Type,
				RefProperty: feature.RefProperty,
				Config:      append(json.RawMessage(nil), feature.Config...),
			})
		}
		cloned.Fields = append(cloned.Fields, clonedField)
	}
	return cloned
}

func normalizeIndexConfigContract(contract interfaces.IndexConfigContract) (interfaces.IndexConfigContract, error) {
	seenFields := make(map[string]struct{}, len(contract.Fields))
	for fieldIndex := range contract.Fields {
		field := &contract.Fields[fieldIndex]
		if field.Name == "" {
			return interfaces.IndexConfigContract{}, fmt.Errorf("index config contract contains an invalid field")
		}
		if _, exists := seenFields[field.Name]; exists {
			return interfaces.IndexConfigContract{}, fmt.Errorf("index config contract contains duplicate field %q", field.Name)
		}
		seenFields[field.Name] = struct{}{}

		seenFeatureTypes := make(map[string]struct{}, len(field.Features))
		for featureIndex := range field.Features {
			feature := &field.Features[featureIndex]
			if feature.Type == "" {
				return interfaces.IndexConfigContract{}, fmt.Errorf("index config contract field %q contains a feature without type", field.Name)
			}
			if _, exists := seenFeatureTypes[feature.Type]; exists {
				return interfaces.IndexConfigContract{}, fmt.Errorf("index config contract field %q has more than one %q feature", field.Name, feature.Type)
			}
			seenFeatureTypes[feature.Type] = struct{}{}
			if feature.RefProperty == field.Name {
				feature.RefProperty = ""
			}
			if len(feature.Config) > 0 {
				config, err := sonic.ConfigStd.Marshal(feature.Config)
				if err != nil {
					return interfaces.IndexConfigContract{}, fmt.Errorf("index config contract field %q feature %q: %w", field.Name, feature.Type, err)
				}
				feature.Config = config
			}
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
func IndexConfigFingerprint(config interfaces.IndexConfigContract) (string, error) {
	data, err := sonic.ConfigStd.Marshal(config)
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
	return sonic.ConfigStd.Marshal(config)
}

func stringConfigValueForFingerprint(config map[string]any, key string) string {
	value, ok := config[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
