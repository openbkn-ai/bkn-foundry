// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package filestore reads datasets from JSON files in the repository.
package filestore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openbkn-ai/bkn-foundry/bkn-eval/internal/domain/entity"
	"github.com/openbkn-ai/bkn-foundry/bkn-eval/internal/port"
)

// Datasets loads dataset files by path.
type Datasets struct{}

var _ port.DatasetSource = Datasets{}

// Load reads a dataset file, rejects unknown fields, and validates it.
func (Datasets) Load(path string) (*entity.Dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ds entity.Dataset
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
