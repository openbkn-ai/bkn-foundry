// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package datasetfile reads datasets from JSON files in the repository.
package datasetfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openbkn-ai/bkn-foundry/bkn-eval/src/domain/valueobject/datasetvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-eval/src/port/driven/idatasetsource"
)

// Store loads dataset files by path.
type Store struct{}

var _ idatasetsource.Source = Store{}

// Load reads a dataset file, rejects unknown fields, and validates it.
func (Store) Load(path string) (*datasetvo.Dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ds datasetvo.Dataset
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ds); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := ds.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &ds, nil
}
