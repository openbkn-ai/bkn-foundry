// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ibusinessresolver

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

type BusinessRef struct {
	RefID         string
	RefType       string
	SourceSystem  string
	VersionStatus string
}

type ResolveRequest struct {
	Scope evidencevo.QueryScope
	Refs  []BusinessRef
}

type Resolution struct {
	RefID        string
	RefType      string
	SourceSystem string
	Visibility   string
	Display      *evidencevo.BusinessDisplay
}

type BusinessResolverPort interface {
	ResolveBusinessRefs(ctx context.Context, request ResolveRequest) ([]Resolution, error)
}
