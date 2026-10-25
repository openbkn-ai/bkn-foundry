// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package isessionstore

import "context"

// RootServiceOptionStore lists names from the same hot receipt membership as
// simple trace identity pages, without reading payloads or computing counts.
type RootServiceOptionStore interface {
	ListTraceRootServiceOptions(context.Context, SummaryPageQuery, string) ([]string, bool, error)
}
