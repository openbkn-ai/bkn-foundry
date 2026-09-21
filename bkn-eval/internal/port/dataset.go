// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package port declares what the domain needs from the outside world. Driven
// adapters implement these interfaces; the command line today and a server
// later are driver adapters that call through them.
package port

import "github.com/openbkn-ai/bkn-foundry/bkn-eval/internal/domain/entity"

// DatasetSource loads a dataset by reference and returns it only if it is
// valid. The command line reads files; a future service reads its database.
type DatasetSource interface {
	Load(ref string) (*entity.Dataset, error)
}
