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
