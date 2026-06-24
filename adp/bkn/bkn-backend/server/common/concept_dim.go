// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

// GlobalConceptVectorDim 是 BKN 全局概念 dataset(adp_bkn_concept_dataset) 的向量字段维度，
// 启动时由 ontology_init.Init 固化。单一全局 dataset 的向量字段维度不可混存，
// 故 CreateKN 校验所选 embedding 模型维度必须等于它。
var GlobalConceptVectorDim int
