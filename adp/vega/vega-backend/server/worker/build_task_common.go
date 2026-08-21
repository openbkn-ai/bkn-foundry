// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/mohae/deepcopy"

	"vega-backend/interfaces"
	"vega-backend/logics"
	resourcelogic "vega-backend/logics/resource"
)

func getIndexName(resourceID, buildTaskID string) string {
	return interfaces.BuildIndexName(resourceID, buildTaskID)
}

// generateDocumentID builds a document ID from ordered build-key values.
func generateDocumentID(keys []interfaces.KeyValue) (string, error) {
	if len(keys) == 0 {
		return "", fmt.Errorf("build document ID: no build key fields")
	}

	for _, key := range keys {
		if key.Value == nil {
			return "", fmt.Errorf("build document ID: build key field %q is null", key.Key)
		}
		if stringValue, ok := key.Value.(string); ok && stringValue == "" {
			return "", fmt.Errorf("build document ID: build key field %q is empty", key.Key)
		}
	}

	serialized, err := sonic.Marshal(keys)
	if err != nil {
		return "", fmt.Errorf("marshal build key values: %w", err)
	}
	sum := sha256.Sum256(serialized)
	return hex.EncodeToString(sum[:]), nil
}

func extractKeyValues(fields []string, document map[string]any) ([]interfaces.KeyValue, error) {
	if document == nil {
		return nil, fmt.Errorf("build document ID: document is required")
	}

	values := make([]interfaces.KeyValue, 0, len(fields))
	for _, field := range fields {
		value, exists := document[field]
		if !exists {
			return nil, fmt.Errorf("build document ID: build key field %q is missing", field)
		}
		values = append(values, interfaces.KeyValue{Key: field, Value: value})
	}
	return values, nil
}

// updateResourceIndexName updates the index name of a resource
func updateResourceIndexName(ctx context.Context, resource *interfaces.Resource, rs interfaces.ResourceService, indexName string) error {
	if resource.LocalIndexName == indexName {
		return nil
	}

	if err := rs.InternalUpdateLocalIndexName(ctx, nil, resource.ID, indexName); err != nil {
		return err
	}
	resource.LocalIndexName = indexName

	return nil
}

func completeBuildTaskWithoutEmbedding(ctx context.Context, resource *interfaces.Resource,
	rs interfaces.ResourceService, bts interfaces.BuildTaskService, taskID, indexName string) error {
	if logics.DB == nil {
		return errors.New("database is not initialized")
	}

	tx, err := logics.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	oldIndexName := resource.LocalIndexName
	if resource.LocalIndexName != indexName {
		if err := rs.InternalUpdateLocalIndexName(ctx, tx, resource.ID, indexName); err != nil {
			return fmt.Errorf("update resource index name: %w", err)
		}
		resource.LocalIndexName = indexName
	}

	completed, err := bts.InternalMarkCompleted(ctx, tx, taskID)
	if err != nil {
		resource.LocalIndexName = oldIndexName
		return fmt.Errorf("update build task status: %w", err)
	}
	if !completed {
		resource.LocalIndexName = oldIndexName
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return fmt.Errorf("rollback completion after build task state changed: %w", err)
		}
		committed = true
		// A stop request may win the race with completion. The resource update
		// has been rolled back, so finish the stopping -> stopped transition.
		if _, err := bts.InternalMarkStopped(ctx, taskID); err != nil {
			return fmt.Errorf("mark build task stopped: %w", err)
		}
		return nil
	}

	if err := tx.Commit(); err != nil {
		resource.LocalIndexName = oldIndexName
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

func cancelBuildTaskForDeletedParent(ctx context.Context, bts interfaces.BuildTaskService, taskID, detail string) error {
	_, err := bts.InternalMarkCancelled(ctx, taskID, detail)
	return err
}

// createManagedLocalIndex creates a build-task local index through LocalIndexManager.
func createManagedLocalIndex(ctx context.Context, lim interfaces.LocalIndexManager, indexName string, buildTask *interfaces.BuildTask, resource *interfaces.Resource) error {
	schema, err := buildLocalIndexSchema(buildTask, resource)
	if err != nil {
		return err
	}
	exist, err := lim.CheckIndexExist(ctx, indexName)
	if err != nil {
		return fmt.Errorf("check local index exist failed: %w", err)
	}
	if exist {
		return nil
	}
	return lim.CreateIndex(ctx, indexName, schema)
}

func buildLocalIndexSchema(buildTask *interfaces.BuildTask, resource *interfaces.Resource) ([]*interfaces.Property, error) {
	var schema []*interfaces.Property
	if resource.SchemaDefinition != nil {
		schemaDefinition, ok := deepcopy.Copy(resource.SchemaDefinition).([]*interfaces.Property)
		if !ok {
			return nil, fmt.Errorf("copy resource schema failed")
		}
		schema = schemaDefinition
	}

	// Legacy resources that have never been updated still contain self-references. Without normalization,
	// dataset resources violate the ref_property restriction and keyword self-references on text fields
	// fail ref type validation (keyword requires string), preventing build task creation. Apply this only
	// to the deep copy above; leave the resource row unchanged so its next update can repair it.
	resourcelogic.NormalizeSelfReferencingFeatures(schema)

	if err := validateBuildTaskSchemaFeatures(resource.Category, schema); err != nil {
		return nil, err
	}
	if err := validateTaskFulltextFeatures(schema, buildTask); err != nil {
		return nil, err
	}
	if err := validateTaskEmbeddingFeatures(schema, buildTask); err != nil {
		return nil, err
	}
	return appendTaskEmbeddingVectorFields(schema, buildTask), nil
}

func validateBuildTaskSchemaFeatures(resourceCategory string, schema []*interfaces.Property) error {
	propsMap := make(map[string]*interfaces.Property, len(schema))
	for _, prop := range schema {
		if prop != nil {
			propsMap[prop.Name] = prop
		}
	}

	for _, prop := range schema {
		if prop == nil {
			continue
		}
		for _, feature := range prop.Features {
			if !resourcelogic.IsFeatureSupported(prop.Type, feature.FeatureType) {
				return fmt.Errorf("resource schema field %q type %q does not support feature type %q", prop.Name, prop.Type, feature.FeatureType)
			}
			if feature.RefProperty == "" {
				continue
			}
			if resourceCategory == interfaces.ResourceCategoryDataset {
				return fmt.Errorf("dataset schema feature on field %q must not set ref_property", prop.Name)
			}
			refProp, exists := propsMap[feature.RefProperty]
			if !exists {
				return fmt.Errorf("resource schema feature on field %q references missing property %q", prop.Name, feature.RefProperty)
			}
			if !resourcelogic.IsFeatureRefPropertyTypeSupported(refProp.Type, feature.FeatureType) {
				return fmt.Errorf("resource schema feature on field %q references property %q type %q incompatible with feature type %q", prop.Name, feature.RefProperty, refProp.Type, feature.FeatureType)
			}
		}
	}
	return nil
}

func appendTaskEmbeddingVectorFields(schema []*interfaces.Property, buildTask *interfaces.BuildTask) []*interfaces.Property {
	newSchema := append([]*interfaces.Property{}, schema...)
	for field, feature := range buildTaskIndexFeatures(buildTask) {
		if feature.Vector == nil {
			continue
		}
		newSchema = append(newSchema, &interfaces.Property{
			Name: interfaces.LocalIndexVectorFieldName(field),
			Type: interfaces.DataType_Vector,
			Features: []interfaces.PropertyFeature{
				{
					FeatureType: interfaces.DataType_Vector,
					Config: map[string]any{
						"dimension": feature.Vector.EmbeddingDim,
						"method": map[string]any{
							"name":   "hnsw",
							"engine": "lucene",
							"parameters": map[string]any{
								"ef_construction": 256,
							},
						},
					},
				},
			},
		})
	}
	return newSchema
}

func buildTaskIndexFeatures(buildTask *interfaces.BuildTask) map[string]interfaces.BuildTaskFieldIndexFeature {
	if buildTask == nil || buildTask.IndexConfig == nil {
		return nil
	}
	return buildTask.IndexConfig.Features
}

func buildTaskHasEmbedding(buildTask *interfaces.BuildTask) bool {
	for _, feature := range buildTaskIndexFeatures(buildTask) {
		if feature.Vector != nil {
			return true
		}
	}
	return false
}

func buildTaskBuildKeyFields(buildTask *interfaces.BuildTask) []string {
	if buildTask == nil || buildTask.IndexConfig == nil {
		return nil
	}
	return append([]string(nil), buildTask.IndexConfig.BuildKeyFields...)
}

// The hasFulltextFeature determines whether a field already has the fulltext feature.
func hasFulltextFeature(prop *interfaces.Property) bool {
	for _, f := range prop.Features {
		if f.FeatureType == interfaces.PropertyFeatureType_Fulltext {
			return true
		}
	}
	return false
}

// analyzerOf returns the analyzer name from fulltext feature configuration, or an empty string when absent.
func analyzerOf(config map[string]any) string {
	if config == nil {
		return ""
	}
	if v, ok := config["analyzer"].(string); ok {
		return v
	}
	return ""
}

func validateTaskFulltextFeatures(schema []*interfaces.Property, buildTask *interfaces.BuildTask) error {
	fulltextConfigs := map[string]*interfaces.BuildTaskFulltextConfig{}
	for field, feature := range buildTaskIndexFeatures(buildTask) {
		if feature.Fulltext != nil {
			fulltextConfigs[field] = feature.Fulltext
		}
	}

	schemaFulltextFields := map[string]struct{}{}
	for _, prop := range schema {
		if prop == nil {
			continue
		}
		for i := range prop.Features {
			feature := &prop.Features[i]
			if feature.FeatureType != interfaces.PropertyFeatureType_Fulltext {
				continue
			}
			fieldName := indexFeatureFieldName(prop, *feature)
			schemaFulltextFields[fieldName] = struct{}{}
			fulltextConfig, ok := fulltextConfigs[fieldName]
			if !ok {
				return fmt.Errorf("resource schema fulltext field %q is not in build task index config", fieldName)
			}
			taskAnalyzer := fulltextConfig.Analyzer
			schemaAnalyzer := analyzerOf(feature.Config)
			if schemaAnalyzer != "" && schemaAnalyzer != taskAnalyzer {
				return fmt.Errorf("resource schema fulltext analyzer %q for field %q does not match build task analyzer %q", schemaAnalyzer, fieldName, taskAnalyzer)
			}
			if taskAnalyzer != "" && schemaAnalyzer == "" {
				feature.Config = map[string]any{"analyzer": taskAnalyzer}
			}
		}
	}
	for field := range fulltextConfigs {
		if _, ok := schemaFulltextFields[field]; !ok {
			return fmt.Errorf("build task fulltext field %q is not in resource schema features", field)
		}
	}
	return nil
}

func validateTaskEmbeddingFeatures(schema []*interfaces.Property, buildTask *interfaces.BuildTask) error {
	embeddingFields := map[string]struct{}{}
	for field, feature := range buildTaskIndexFeatures(buildTask) {
		if feature.Vector != nil {
			embeddingFields[field] = struct{}{}
		}
	}

	schemaEmbeddingFields := map[string]struct{}{}
	for _, prop := range schema {
		if prop == nil {
			continue
		}
		for _, feature := range prop.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector {
				continue
			}
			fieldName := indexFeatureFieldName(prop, feature)
			schemaEmbeddingFields[fieldName] = struct{}{}
			if _, ok := embeddingFields[fieldName]; !ok {
				return fmt.Errorf("resource schema embedding field %q is not in build task index config", fieldName)
			}
		}
	}
	for field := range embeddingFields {
		if _, ok := schemaEmbeddingFields[field]; !ok {
			return fmt.Errorf("build task embedding field %q is not in resource schema features", field)
		}
	}
	return nil
}

func indexFeatureFieldName(prop *interfaces.Property, feature interfaces.PropertyFeature) string {
	if feature.RefProperty != "" {
		return feature.RefProperty
	}
	return prop.Name
}
