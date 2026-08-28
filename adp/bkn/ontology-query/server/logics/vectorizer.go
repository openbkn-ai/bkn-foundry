// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"

	cond "ontology-query/common/condition"
)

// Vectorizer converts query terms to vectors for condition rewriting and DSL conversion.
type Vectorizer func(ctx context.Context, property *cond.DataProperty, word string) ([]cond.VectorResp, error)

// MemoizeVectorizer deduplicates vectorization within one request by (model, text).
//
// Each knn sub-condition in an OR condition would vectorize independently, while in one search the query term is the same sentence
// and often the same model as well. The vector is necessarily identical, but several extra model-call round trips are paid. The more object type fields
// and vector-indexed fields there are, the larger this waste becomes.
//
// Cache only inside the single-request closure. Do not cache across requests to avoid sending stale vectors after the model changes.
func MemoizeVectorizer(vectorize Vectorizer) Vectorizer {
	type key struct {
		model string
		word  string
	}
	type entry struct {
		vectors []cond.VectorResp
		err     error
	}

	cache := map[key]entry{}
	return func(ctx context.Context, property *cond.DataProperty, word string) ([]cond.VectorResp, error) {
		if property == nil {
			return vectorize(ctx, property, word)
		}

		model := property.MappedField.Name
		// Do not cache when the model is unknown. Without a distinguishing key, prefer recalculation over mixing vectors from different models.
		if model == "" {
			return vectorize(ctx, property, word)
		}

		k := key{model: model, word: word}
		if cached, ok := cache[k]; ok {
			return cached.vectors, cached.err
		}

		vectors, err := vectorize(ctx, property, word)
		cache[k] = entry{vectors: vectors, err: err}
		return vectors, err
	}
}
