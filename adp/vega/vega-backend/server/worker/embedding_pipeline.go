// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"fmt"
	"time"

	"vega-backend/interfaces"
)

const (
	embeddingPipelineMaxAttempts = 3
)

// embeddingPipeline enriches a bounded source batch in the build execution path.
// It deliberately owns no task lifecycle, queue, Kafka topic, or consumer offset.
type embeddingPipeline struct {
	mfs   interfaces.ModelFactoryService
	sleep func(time.Duration)
}

func (p *embeddingPipeline) enrich(ctx context.Context, documents map[string]map[string]any,
	config map[string]*interfaces.SmallModel) error {
	if len(config) == 0 || len(documents) == 0 {
		return nil
	}
	if p == nil || p.mfs == nil {
		return fmt.Errorf("embedding pipeline is not initialized")
	}
	for field, fieldConfig := range config {
		if fieldConfig == nil || fieldConfig.ModelID == "" {
			return fmt.Errorf("embedding model is required for vector field %q", field)
		}
		texts := make([]string, 0, len(documents))
		targets := make([]map[string]any, 0, len(documents))
		for _, document := range documents {
			text, ok := document[field].(string)
			if !ok || text == "" {
				continue
			}
			texts = append(texts, text)
			targets = append(targets, document)
		}
		vectors, err := p.getVectorsWithRetry(ctx, fieldConfig, texts)
		if err != nil {
			return err
		}
		if len(vectors) != len(texts) {
			return fmt.Errorf("get vector: got %d vectors for %d texts", len(vectors), len(texts))
		}
		for i, vector := range vectors {
			if vector != nil && vector.Vector != nil {
				targets[i][field+"_vector"] = vector.Vector
			}
		}
	}
	return nil
}

func (p *embeddingPipeline) getVectorsWithRetry(ctx context.Context, model *interfaces.SmallModel, words []string) ([]*interfaces.VectorResp, error) {
	var lastErr error
	for attempt := 1; attempt <= embeddingPipelineMaxAttempts; attempt++ {
		vectors, err := p.mfs.GetVector(ctx, model, words)
		if err == nil {
			return vectors, nil
		}
		lastErr = err
		if attempt < embeddingPipelineMaxAttempts {
			if p.sleep != nil {
				p.sleep(time.Duration(interfaces.BUILD_TASK_RETRY_INTERVAL) * time.Second)
			} else {
				time.Sleep(time.Duration(interfaces.BUILD_TASK_RETRY_INTERVAL) * time.Second)
			}
		}
	}
	return nil, fmt.Errorf("get vector after %d attempts: %w", embeddingPipelineMaxAttempts, lastErr)
}

func buildTaskEmbeddingConfig(buildTask *interfaces.BuildTask) map[string]*interfaces.SmallModel {
	config := map[string]*interfaces.SmallModel{}
	for field, feature := range buildTaskIndexFeatures(buildTask) {
		if feature.Vector != nil {
			config[field] = feature.Vector
		}
	}
	return config
}
