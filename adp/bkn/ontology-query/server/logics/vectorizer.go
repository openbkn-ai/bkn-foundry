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

// Vectorizer 把查询词转成向量，供条件改写与 DSL 转换使用。
type Vectorizer func(ctx context.Context, property *cond.DataProperty, word string) ([]cond.VectorResp, error)

// MemoizeVectorizer 在一次请求内按 (模型, 文本) 去重向量化。
//
// 一条 OR 条件里每个 knn 子条件都会各自向量化一次，而同一次检索里查询词是同一句、
// 模型往往也是同一个——向量必然相同，却要多付几次模型调用的往返。对象类字段越多、
// 建了向量索引的字段越多，这笔浪费越大。
//
// 只在单次请求的闭包里缓存：不做跨请求缓存，免得模型换了之后还发旧向量。
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

		model := ""
		if property.IndexConfig != nil {
			model = property.IndexConfig.VectorConfig.ModelID
		}
		// 模型未知时不缓存：拿不到区分依据，宁可多算也不能把不同模型的向量混用。
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
