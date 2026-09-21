// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package metric

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "ontology-query/common/condition"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	rowfilter "ontology-query/logics/row_filter"
)

// resolveRowFilter always resolves against the original caller carried by the
// trusted proxy context. Metrics execute through their managed proxy, but that
// account must never become the subject whose row policy is evaluated.
func (s *metricQueryService) resolveRowFilter(ctx context.Context, knID string,
	objectType interfaces.ObjectType) (*cond.CondCfg, bool, error) {
	if s.rowFilters == nil {
		return nil, false, metricRowFilterUnavailable(ctx, fmt.Errorf("row-filter resolver is not configured"))
	}
	ref := knID + "/" + objectType.OTID
	decisions, err := s.rowFilters.ResolveRowFilters(ctx, []string{ref})
	if err != nil {
		return nil, false, err
	}
	if len(decisions) != 1 || decisions[0].ObjectTypeRef != ref ||
		strings.TrimSpace(decisions[0].EffectiveRowFilterDigest) == "" {
		return nil, false, metricRowFilterUnavailable(ctx, fmt.Errorf("row-filter decision response mismatch"))
	}
	condition, _, noResults, err := rowfilter.Compile(decisions[0].Predicate, objectType)
	if err != nil {
		return nil, false, metricRowFilterUnavailable(ctx, err)
	}
	return condition, noResults, nil
}

func metricRowFilterUnavailable(ctx context.Context, err error) error {
	return rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_Metric_InternalError_QueryFailed).
		WithErrorDetails(err.Error())
}
