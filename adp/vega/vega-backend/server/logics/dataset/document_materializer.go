package dataset

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

type pendingEmbedding struct {
	field     string
	text      string
	dimension int
}

func (ds *datasetService) materializeDocument(ctx context.Context, res *interfaces.Resource, document map[string]any) (map[string]any, error) {
	// Protect the internal service boundary: a caller must not write when the
	// Dataset resource has already lost its physical index.
	if res == nil || strings.TrimSpace(res.LocalIndexName) == "" {
		return nil, invalidDocumentError(ctx, "dataset resource has no available local index")
	}
	// A nil map cannot be materialized or sent to the local index.
	if document == nil {
		return nil, invalidDocumentError(ctx, "document is required")
	}
	result := make(map[string]any, len(document))
	for key, value := range document {
		result[key] = value
	}
	models := map[string]*interfaces.SmallModel{}
	pending := map[string][]pendingEmbedding{}
	modelOrder := make([]string, 0)

	for _, prop := range res.SchemaDefinition {
		// Persisted schemas never contain nil properties. Keep this guard for
		// internal callers that construct Resource values directly.
		if prop == nil {
			continue
		}
		for _, feature := range prop.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector {
				continue
			}
			dimension, err := vectorFeatureDimension(feature.Config, prop.Name)
			if err != nil {
				return nil, invalidDocumentError(ctx, err.Error())
			}
			if prop.Type == interfaces.DataType_Vector {
				if value, exists := result[prop.Name]; exists {
					if err := validateVector(value, dimension, prop.Name); err != nil {
						return nil, invalidDocumentError(ctx, err.Error())
					}
				}
				continue
			}
			outputField := interfaces.LocalIndexVectorFieldName(prop.Name)
			if value, exists := result[outputField]; exists {
				// An explicit vector wins over inference, but validate it before it
				// reaches OpenSearch.
				if err := validateVector(value, dimension, outputField); err != nil {
					return nil, invalidDocumentError(ctx, err.Error())
				}
				continue
			}
			if text, ok := result[prop.Name].(string); ok && strings.TrimSpace(text) != "" {
				modelID, _ := feature.Config["embedding_model"].(string)
				if modelID == "" {
					modelID = res.IndexConfig.DefaultEmbeddingModel
				}
				if _, exists := models[modelID]; !exists {
					model, err := ds.mfs.GetModelByID(ctx, modelID)
					if err != nil {
						// The registry is an external dependency. Abort before any local
						// index write so the document is never partially materialized.
						return nil, fmt.Errorf("get embedding model %q: %w", modelID, err)
					}
					models[modelID] = model
					modelOrder = append(modelOrder, modelID)
				}
				if _, exists := pending[modelID]; !exists {
					pending[modelID] = []pendingEmbedding{}
				}
				pending[modelID] = append(pending[modelID], pendingEmbedding{field: outputField, text: text, dimension: dimension})
			}
		}
	}

	for _, modelID := range modelOrder {
		requests := pending[modelID]
		if len(requests) == 0 {
			continue
		}
		words := make([]string, len(requests))
		for i, request := range requests {
			words[i] = request.text
		}
		vectors, err := ds.mfs.GetVector(ctx, models[modelID], words)
		if err != nil {
			// Inference is an external side effect; do not write a document when
			// it fails.
			return nil, fmt.Errorf("generate embeddings for model %q: %w", modelID, err)
		}
		// The response must still match the request after crossing the model
		// service boundary, otherwise fields could receive the wrong vectors.
		if len(vectors) != len(requests) {
			return nil, fmt.Errorf("embedding model %q returned %d vectors for %d fields", modelID, len(vectors), len(requests))
		}
		for i, vector := range vectors {
			// A malformed model response must not produce a partially indexed
			// document.
			if vector == nil {
				return nil, fmt.Errorf("embedding model %q returned an empty vector", modelID)
			}
			if err := validateVector(vector.Vector, requests[i].dimension, requests[i].field); err != nil {
				return nil, fmt.Errorf("embedding model %q returned invalid vector: %w", modelID, err)
			}
			result[requests[i].field] = vector.Vector
		}
	}
	return result, nil
}

// vectorFeatureDimension validates persisted schema data before it controls
// document processing. Legacy rows may predate the dimension field entirely.
func vectorFeatureDimension(config map[string]any, field string) (int, error) {
	value, ok := config["dimension"]
	if !ok {
		return 0, fmt.Errorf("vector feature for field %q has no valid dimension", field)
	}
	dimension, ok := value.(float64)
	if !ok || math.IsNaN(dimension) || math.IsInf(dimension, 0) || dimension <= 0 || math.Trunc(dimension) != dimension {
		return 0, fmt.Errorf("vector feature for field %q has no valid dimension", field)
	}
	return int(dimension), nil
}

// validateVector protects OpenSearch from invalid caller input or an invalid
// model-service response. The expected dimension itself is already guaranteed
// by the persisted Resource schema.
func validateVector(value any, dimension int, field string) error {
	vector, ok := value.([]float32)
	if !ok {
		values, ok := value.([]any)
		if !ok {
			return fmt.Errorf("vector field %q must be an array", field)
		}
		vector = make([]float32, len(values))
		for i, value := range values {
			number, ok := value.(float64)
			if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
				return fmt.Errorf("vector field %q contains a non-finite number", field)
			}
			vector[i] = float32(number)
		}
	}
	if len(vector) != dimension {
		return fmt.Errorf("vector field %q has dimension %d, expected %d", field, len(vector), dimension)
	}
	for _, number := range vector {
		if math.IsNaN(float64(number)) || math.IsInf(float64(number), 0) {
			return fmt.Errorf("vector field %q contains a non-finite number", field)
		}
	}
	return nil
}

func invalidDocumentError(ctx context.Context, details string) error {
	return rest.NewHTTPError(ctx, 400, verrors.VegaBackend_InvalidParameter_RequestBody).WithErrorDetails(details)
}
