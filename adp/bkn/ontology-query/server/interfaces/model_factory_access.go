// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"

	cond "ontology-query/common/condition"
)

const (
	SMALL_MODEL_TYPE_EMBEDDING = "embedding"
)

type SmallModel struct {
	ModelID      string `json:"model_id"`
	ModelName    string `json:"model_name"`
	ModelType    string `json:"model_type"`
	EmbeddingDim int    `json:"embedding_dim"`
	BatchSize    int    `json:"batch_size"`
	MaxTokens    int    `json:"max_tokens"`
}

// ModelFactoryAccess defines the model-factory access interface.
//
//go:generate mockgen -source ../interfaces/model_factory_access.go -destination ../interfaces/mock/mock_model_factory_access.go
type ModelFactoryAccess interface {
	// GetVector returns one vector for each input string.
	// Parameters:
	//   - ctx: request context
	//   - texts: input strings
	// Returns:
	//   - [][]float32: an equally sized vector slice, one vector per input string
	//   - error: any retrieval error
	GetVector(ctx context.Context, model *SmallModel, words []string) ([]cond.VectorResp, error)

	GetModelByID(ctx context.Context, modelID string) (*SmallModel, error)
}
