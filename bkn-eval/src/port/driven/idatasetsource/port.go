// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package idatasetsource is the driven port for reading datasets.
package idatasetsource

import "github.com/openbkn-ai/bkn-foundry/bkn-eval/src/domain/valueobject/datasetvo"

// Source loads a dataset by reference and returns it only if it is valid.
// The command line reads files; a future service reads its database.
type Source interface {
	Load(ref string) (*datasetvo.Dataset, error)
}
