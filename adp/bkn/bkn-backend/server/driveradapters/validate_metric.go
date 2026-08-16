// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/dlclark/regexp2"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "bkn-backend/common/condition"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

var validMetricTypesEnum = map[string]struct{}{
	interfaces.MetricTypeAtomic:    {},
	interfaces.MetricTypeDerived:   {},
	interfaces.MetricTypeComposite: {},
}

var validMetricTimeRangePolicies = map[string]struct{}{
	interfaces.MetricTimeDefaultRangePolicyLast1h:      {},
	interfaces.MetricTimeDefaultRangePolicyLast24h:     {},
	interfaces.MetricTimeDefaultRangePolicyCalendarDay: {},
	interfaces.MetricTimeDefaultRangePolicyNone:        {},
}

func metricInvalidDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(
		rest.GetLanguageByCtx(ctx),
		"BknBackend.Metric.InvalidParameter.Detail."+name,
		templateData,
	)
}

// ValidateMetricRequests validates batch metric creation requests without writing data or resolving dependencies.
func ValidateMetricRequests(ctx context.Context, entries []*interfaces.MetricDefinition, strictMode bool) error {
	if len(entries) == 0 {
		return nil
	}
	seenName := make(map[string]struct{}, len(entries))
	seenID := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "NameRequired", nil))
		}
		if _, dup := seenName[name]; dup {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "DuplicatedName", map[string]any{"name": name}))
		}
		seenName[name] = struct{}{}

		id := strings.TrimSpace(e.ID)
		if id != "" {
			if _, dup := seenID[id]; dup {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
					WithErrorDetails(metricInvalidDetail(ctx, "DuplicatedID", map[string]any{"id": id}))
			}
			seenID[id] = struct{}{}
		}

		if err := ValidateMetricRequest(ctx, e, strictMode); err != nil {
			return err
		}
	}
	return nil
}

// ValidateMetricRequest validates a single metric creation request.
func ValidateMetricRequest(ctx context.Context, metric *interfaces.MetricDefinition, strictMode bool) error {
	if err := validateID(ctx, strings.TrimSpace(metric.ID)); err != nil {
		return err
	}
	if err := validateObjectName(ctx, strings.TrimSpace(metric.Name), interfaces.MODULE_TYPE_METRIC); err != nil {
		return err
	}
	if err := ValidateTags(ctx, metric.Tags); err != nil {
		return err
	}
	if err := validateMetricType(ctx, strings.TrimSpace(metric.MetricType), strictMode); err != nil {
		return err
	}
	if err := validateMetricUnits(ctx, strings.TrimSpace(metric.UnitType), strings.TrimSpace(metric.Unit), strictMode); err != nil {
		return err
	}
	if err := validateMetricScopeBody(ctx, strings.TrimSpace(metric.ScopeType), strings.TrimSpace(metric.ScopeRef), strictMode); err != nil {
		return err
	}
	if err := validateMetricTimeDimensionBody(ctx, metric.TimeDimension, strictMode); err != nil {
		return err
	}
	if err := validateMetricCalculationFormula(ctx, metric.CalculationFormula, strictMode); err != nil {
		return err
	}
	if err := validateMetricAnalysisDimensionsBody(ctx, metric.AnalysisDimensions, strictMode); err != nil {
		return err
	}
	return nil
}

func validateMetricCalculationFormula(ctx context.Context, f *interfaces.MetricCalculationFormula, strictMode bool) error {
	if f == nil {
		if strictMode {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "CalculationFormulaRequired", nil))
		}
		return nil
	}
	if f.Condition != nil {
		if err := validateMetricCond(ctx, f.Condition); err != nil {
			return err
		}
	}
	if err := validateMetricAggregation(ctx, &f.Aggregation, strictMode); err != nil {
		return err
	}
	for i := range f.GroupBy {
		p := strings.TrimSpace(f.GroupBy[i].Property)
		if p == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "GroupByPropertyRequired", map[string]any{"index": i}))
		}
		if err := ValidatePropertyName(ctx, p); err != nil {
			return err
		}
	}
	for i := range f.OrderBy {
		p := strings.TrimSpace(f.OrderBy[i].Property)
		if p == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "OrderByPropertyRequired", map[string]any{"index": i}))
		}
		if err := ValidatePropertyName(ctx, p); err != nil {
			return err
		}
		d := strings.TrimSpace(f.OrderBy[i].Direction)
		if d != "" && d != interfaces.MetricOrderDirectionAsc && d != interfaces.MetricOrderDirectionDesc {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "OrderByDirectionInvalid", nil))
		}
	}
	if f.Having != nil {
		if err := validateMetricHaving(ctx, f.Having); err != nil {
			return err
		}
	}
	return nil
}

func validateMetricType(ctx context.Context, metricType string, strictMode bool) error {
	if strictMode {
		if metricType == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "MetricTypeRequired", nil))
		}
		if metricType != interfaces.MetricTypeAtomic {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidMetricType)
		}
		return nil
	}
	// In non-strict mode, an empty metric type is allowed for incremental imports.
	if metricType == "" {
		return nil
	}
	// In non-strict mode, a provided value must still be valid.
	if _, ok := validMetricTypesEnum[metricType]; !ok {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidDetail(ctx, "MetricTypeInvalid", nil))
	}
	return nil
}

func validateMetricUnits(ctx context.Context, unitType string, unit string, strictMode bool) error {
	if strictMode {
		if unitType == "" || unit == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "UnitsRequired", nil))
		}
	}
	if unitType != "" {
		if _, ok := interfaces.ValidMetricUnitTypes[unitType]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "UnitTypeInvalid", map[string]any{"unitType": unitType, "allowed": interfaces.ValidMetricUnitTypesArr}))
		}
	}
	if unit != "" {
		if _, ok := interfaces.ValidMetricUnits[unit]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "UnitInvalid", map[string]any{"unit": unit, "allowed": interfaces.ValidMetricUnitsArr}))
		}
	}
	return nil
}

// Validate scope_type and scope_ref. Non-strict imports may omit both fields.
func validateMetricScopeBody(ctx context.Context, scopeType, scopeRef string, strictMode bool) error {
	if strictMode {
		if scopeType == "" || scopeRef == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "ScopeRequired", nil))
		}
		if scopeType != interfaces.ScopeTypeObjectType {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "ScopeTypeUnsupported", nil))
		}
		return nil
	}
	if scopeType == "" && scopeRef == "" {
		return nil
	}
	if scopeType != interfaces.ScopeTypeObjectType {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidDetail(ctx, "ScopeTypeUnsupported", nil))
	}
	if scopeRef == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidDetail(ctx, "ScopeRefRequired", nil))
	}
	return nil
}

func timeDimensionPresent(td *interfaces.MetricTimeDimension) bool {
	if td == nil {
		return false
	}
	return strings.TrimSpace(td.Property) != "" || strings.TrimSpace(td.DefaultRangePolicy) != ""
}

func validateMetricTimeDimensionBody(ctx context.Context, td *interfaces.MetricTimeDimension, _ bool) error {
	// A missing or incomplete time dimension is optional.
	if td == nil || !timeDimensionPresent(td) {
		return nil
	}
	// A provided time dimension must be valid.
	prop := strings.TrimSpace(td.Property)
	if prop == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidDetail(ctx, "TimeDimensionPropertyRequired", nil))
	}
	pol := strings.TrimSpace(td.DefaultRangePolicy)
	if pol != "" {
		if _, ok := validMetricTimeRangePolicies[pol]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "TimeDimensionPolicyInvalid", nil))
		}
	}
	return nil
}

func validateMetricAnalysisDimensionsBody(ctx context.Context, ads []interfaces.MetricAnalysisDimension, strictMode bool) error {
	_ = strictMode
	if len(ads) == 0 {
		return nil
	}
	for i := range ads {
		n := strings.TrimSpace(ads[i].Name)
		if n == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "AnalysisDimensionNameRequired", map[string]any{"index": i}))
		}
		if ads[i].DisplayName != "" {
			if err := validateObjectName(ctx, ads[i].DisplayName, interfaces.MODULE_TYPE_METRIC); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMetricAggregation(ctx context.Context, a *interfaces.MetricAggregation, strictMode bool) error {
	if a == nil {
		if strictMode {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidDetail(ctx, "AggregationRequired", nil))
		}
		return nil
	}
	if strings.TrimSpace(a.Property) == "" || strings.TrimSpace(a.Aggr) == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidDetail(ctx, "AggregationFieldsRequired", nil))
	}
	ag := strings.TrimSpace(a.Aggr)
	if _, ok := interfaces.ValidMetricAggrs[ag]; !ok {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidDetail(ctx, "AggregationInvalid", map[string]any{"aggregation": ag}))
	}
	return nil
}

func validateMetricHaving(ctx context.Context, h *interfaces.MetricHaving) error {
	// Default an empty having field to __value.
	if strings.TrimSpace(h.Field) == "" {
		h.Field = interfaces.MetricHavingFieldValue
	}
	// A having operation is required.
	if strings.TrimSpace(h.Operation) == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidDetail(ctx, "HavingOperationRequired", nil))
	}

	// A having value is required.
	if h.Value == nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidDetail(ctx, "HavingValueRequired", nil))
	}

	// The having value must be numeric.
	switch v := h.Value.(type) {
	case int, int8, int16, int32, int64, float32, float64:
		return nil
	default:
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidDetail(ctx, "HavingValueTypeInvalid", map[string]any{"type": fmt.Sprintf("%T", v)}))
	}
}

func validateMetricCond(ctx context.Context, cfg *cond.CondCfg) error {
	if cfg == nil {
		return nil
	}

	// Validate the filter operation.
	if cfg.Operation == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_NullParameter_ConditionOperation)
	}

	_, exists := cond.OperationMap[cfg.Operation]
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_UnsupportConditionOperation)
	}

	// Metric conditions do not support fuzzy or semantic query operators.
	switch cfg.Operation {
	case cond.OperationAnd, cond.OperationOr:
		if len(cfg.SubConds) == 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Condition).
				WithErrorDetails(metricInvalidDetail(ctx, "ConditionSubconditionsRequired", map[string]any{"operation": cfg.Operation}))
		}
		if len(cfg.SubConds) > cond.MaxSubCondition {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_CountExceeded_Conditions).
				WithErrorDetails(metricInvalidDetail(ctx, "ConditionSubconditionsExceeded", map[string]any{"limit": cond.MaxSubCondition}))
		}

		for _, subCond := range cfg.SubConds {
			if err := validateMetricCond(ctx, subCond); err != nil {
				return err
			}
		}
		return nil
	default:
		// A filter field is required for leaf conditions.
		if cfg.Field == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_NullParameter_ConditionName)
		}
	}

	switch cfg.Operation {
	case cond.OperationEq, cond.OperationNotEq, cond.OperationGt, cond.OperationGte, cond.OperationLt, cond.OperationLte,
		cond.OperationLike, cond.OperationNotLike, cond.OperationPrefix, cond.OperationNotPrefix, cond.OperationRegex,
		cond.OperationCurrent:
		// These operators require a single value.
		_, ok := cfg.Value.([]any)
		if ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(metricInvalidDetail(ctx, "ConditionSingleValueRequired", map[string]any{"operation": cfg.Operation}))
		}

		if cfg.Operation == cond.OperationLike || cfg.Operation == cond.OperationNotLike ||
			cfg.Operation == cond.OperationPrefix || cfg.Operation == cond.OperationNotPrefix {
			_, ok := cfg.Value.(string)
			if !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
					WithErrorDetails(metricInvalidDetail(ctx, "ConditionStringValueRequired", nil))
			}
		}

		if cfg.Operation == cond.OperationRegex {
			val, ok := cfg.Value.(string)
			if !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
					WithErrorDetails(metricInvalidDetail(ctx, "ConditionRegexStringRequired", nil))
			}

			_, err := regexp2.Compile(val, regexp2.RE2)
			if err != nil {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
					WithErrorDetails(metricInvalidDetail(ctx, "ConditionRegexInvalid", nil))
			}

		}

	case cond.OperationIn, cond.OperationNotIn:
		// The in and not_in operators require a non-empty array of primitive values.
		_, ok := cfg.Value.([]any)
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(metricInvalidDetail(ctx, "ConditionArrayRequired", nil))
		}

		if len(cfg.Value.([]any)) <= 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(metricInvalidDetail(ctx, "ConditionArrayNonEmpty", nil))
		}
	case cond.OperationRange, cond.OperationOutRange, cond.OperationBefore, cond.OperationBetween:
		// Range-like operators require a two-element array.
		// out_range excludes the interval between the two values.
		v, ok := cfg.Value.([]any)
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(metricInvalidDetail(ctx, "ConditionRangeArrayRequired", nil))
		}

		if len(v) != 2 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(metricInvalidDetail(ctx, "ConditionRangeArrayLength", nil))
		}
	default:
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_UnsupportConditionOperation).
			WithErrorDetails(metricInvalidDetail(ctx, "ConditionOperationUnsupported", map[string]any{"operation": cfg.Operation}))
	}
	return nil
}
