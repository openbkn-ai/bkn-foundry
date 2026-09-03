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

	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

// Parameter verification for resource data query
func ValidateResourceDataQueryParams(ctx context.Context, params *interfaces.ResourceDataQueryParams) error {
	if params.Paging.Cursor != "" {
		return validateResourceDataCursorContinuation(ctx, params)
	}

	// Verify whether the format is original or flat
	if params.Format == "" {
		params.Format = interfaces.Format_Original
	} else {
		err := validateFormat(ctx, params.Format)
		if err != nil {
			return err
		}
	}

	err := validateResourceDataPaging(ctx, params)
	if err != nil {
		return err
	}

	// Verify the sorting parameter
	err = validateSortFields(ctx, params.Sort)
	if err != nil {
		return err
	}

	// Parameter verification in Aggregation mode: It is performed when any of the parameters Aggregation, GroupBy, or Having exists
	if isAggregateQuery(params) {
		err = validateAggregateParams(ctx, params)
		if err != nil {
			return err
		}
	}

	// The filter conditions are connected using a map and then decoded into the condCfg
	var actualCond *interfaces.FilterCondCfg
	err = mapstructure.Decode(params.FilterCondition, &actualCond)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterCondition).
			WithErrorDetails(fmt.Sprintf("mapstructure decode filters failed: %s", err.Error()))
	}
	params.FilterCondCfg = actualCond

	// Verify the global filtering conditions: whether the operators, field types, and operators match
	err = validateFilterCondCfg(ctx, params.FilterCondCfg)
	if err != nil {
		return err
	}

	return nil
}

func validateResourceDataPaging(ctx context.Context, params *interfaces.ResourceDataQueryParams) error {
	paging := params.Paging
	// Backward compatibility: map deprecated top-level limit/offset into paging
	// when the caller has not provided a paging object (foundry#475).
	if paging.Cursor == "" && paging.Mode == "" && paging.Limit == 0 && paging.Offset == 0 {
		if params.LegacyLimit != 0 || params.LegacyOffset != 0 {
			paging.Limit = params.LegacyLimit
			paging.Offset = params.LegacyOffset
		}
	}
	if paging.Mode == interfaces.PagingModeCursor && paging.Limit == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Limit).
			WithErrorDetails("paging.limit is required for cursor paging")
	}
	params.Paging = paging.Normalized()
	params.Offset = params.Paging.Offset
	params.Limit = params.Paging.Limit
	params.LegacyLimit = 0
	params.LegacyOffset = 0
	if params.Paging.Mode == interfaces.PagingModeCursor {
		if params.Offset < 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Offset).
				WithErrorDetails("paging.offset must not be negative")
		}
		if params.Limit < interfaces.MinPageLimit || params.Limit > interfaces.MaxPageLimit {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Limit).
				WithErrorDetails(fmt.Sprintf("paging.limit must be in the range of [%d,%d] for cursor paging", interfaces.MinPageLimit, interfaces.MaxPageLimit))
		}
		if params.Paging.KeepAliveSec != 0 && (params.Paging.KeepAliveSec < interfaces.MinCursorKeepAliveSec || params.Paging.KeepAliveSec > interfaces.MaxCursorKeepAliveSec) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
				WithErrorDetails(fmt.Sprintf("paging.keep_alive_sec must be between %d and %d", interfaces.MinCursorKeepAliveSec, interfaces.MaxCursorKeepAliveSec))
		}
		return nil
	}
	if params.Paging.Mode != interfaces.PagingModeSingle {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("paging.mode must be either single or cursor")
	}
	return validatePaginationParams(ctx, params.Offset, params.Limit)
}

func validateResourceDataCursorContinuation(ctx context.Context, params *interfaces.ResourceDataQueryParams) error {
	paging := params.Paging
	if paging.Mode != "" || paging.Offset != 0 || paging.Limit != 0 || paging.KeepAliveSec != 0 ||
		params.FilterCondition != nil || len(params.Sort) != 0 || len(params.OutputFields) != 0 ||
		params.Aggregation != nil || len(params.GroupBy) != 0 || params.Having != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor continuation must contain only paging.cursor")
	}
	params.Offset = 0
	params.Limit = 0
	// The initial request freezes this value in the cursor session. A value on
	// continuation is accepted for request-shape consistency but cannot change it.
	params.NeedTotal = false
	return nil
}

func validateFormat(ctx context.Context, format string) error {
	if format != interfaces.Format_Original && format != interfaces.Format_Flat {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails(fmt.Sprintf("The output format should be %s or %s", interfaces.Format_Original, interfaces.Format_Flat))
	}

	return nil
}

// Pagination sorting parameter verification
func validatePaginationParams(ctx context.Context, offset, limit int) error {
	// from + size query verification
	if offset < 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Offset).
			WithErrorDetails("When execute From + size query, 'offset' should be >= 0")
	}

	if limit < interfaces.MIN_LIMIT || limit > interfaces.MAX_SEARCH_SIZE {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Limit).
			WithErrorDetails(fmt.Sprintf("Limit should be in the range of [%d,%d]", interfaces.MIN_LIMIT, interfaces.MAX_SEARCH_SIZE))
	}

	return nil
}

func validateSortFields(ctx context.Context, sortFields []*interfaces.SortField) error {
	for _, sortField := range sortFields {
		if sortField.Direction != interfaces.ASC_DIRECTION &&
			sortField.Direction != interfaces.DESC_DIRECTION {

			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Direction).
				WithErrorDetails("The sort direction should be desc or asc")
		}
	}

	return nil
}

func validateFilterCondCfg(ctx context.Context, cfg *interfaces.FilterCondCfg) error {
	if cfg == nil {
		return nil
	}

	// Determine whether the filter is an empty object {}
	if cfg.Name == "" && cfg.Operation == "" && len(cfg.SubConds) == 0 && cfg.ValueFrom == "" && cfg.Value == nil {
		return nil
	}

	// Filtering operator
	if cfg.Operation == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_NullParameter_FilterConditionOperation)
	}

	condFactory, exists := filter_condition.OperationMap[cfg.Operation]
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_UnsupportFilterConditionOperation).
			WithErrorDetails(fmt.Sprintf("Unsupported filter condition operation: %s", cfg.Operation))
	}

	if !condFactory.SupportSubCond() {
		if len(cfg.SubConds) > 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_UnsupportFilterConditionOperation).
				WithErrorDetails(fmt.Sprintf("operation '%s' does not support sub conditions", cfg.Operation))
		}
	} else {
		if len(cfg.SubConds) > interfaces.MaxSubCondition {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_CountExceeded_FilterConditionSubConds).
				WithErrorDetails(fmt.Sprintf("The number of subConditions exceeds %d", interfaces.MaxSubCondition))
		}

		for _, subCond := range cfg.SubConds {
			err := validateFilterCondCfg(ctx, subCond)
			if err != nil {
				return err
			}
		}
	}

	if condFactory.NeedName() {
		// The name of the filter field cannot be empty
		if cfg.Name == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_NullParameter_FilterConditionName)
		}
	}

	if condFactory.NeedValue() {
		if cfg.Value == nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_NullParameter_FilterConditionValue)
		}

		if cfg.ValueFrom == "" {
			cfg.ValueFrom = interfaces.ValueFrom_Const
		}
		if condFactory.NeedConstValue() {
			// The value of the filter field cannot be empty
			if cfg.ValueFrom != interfaces.ValueFrom_Const {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValueFrom)
			}
		}

		if condFactory.IsSingleValue() {
			// The value on the right is a single value
			if _, ok := cfg.Value.([]any); ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
					WithErrorDetails(fmt.Sprintf("[%s] operation's value should be a single value", cfg.Operation))
			}
		} else if condFactory.IsFixedLenArrayValue() {
			// The value on the right is an array value
			if vals, ok := cfg.Value.([]any); !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
					WithErrorDetails(fmt.Sprintf("[%s] operation's value must be an array", cfg.Operation))
			} else {
				if condFactory.IsFixedLenArrayValue() && len(vals) != condFactory.RequiredValueLen() {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
						WithErrorDetails(fmt.Sprintf("[%s] operation's value must contain %d values", cfg.Operation, condFactory.RequiredValueLen()))
				} else if len(vals) == 0 {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
						WithErrorDetails(fmt.Sprintf("[%s] operation's value should contains at least 1 value", cfg.Operation))
				}
				if cfg.Operation == filter_condition.OperationBefore {
					if err := filter_condition.NormalizeBeforeInterval(vals); err != nil {
						return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_FilterConditionValue).
							WithErrorDetails(err.Error())
					}
				}
			}
		}
	}

	return nil
}

// isAggregateQuery determines whether it is an aggregated query
func isAggregateQuery(params *interfaces.ResourceDataQueryParams) bool {
	// Infer based on the aggregation-related fields
	return params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil
}

// validateCalendarInterval verifies whether calendar_interval is a valid enumeration value
// The allowed values include: minute, hour, day, week, month, quarter, year
func validateCalendarInterval(ctx context.Context, calendarInterval string) error {
	switch calendarInterval {
	case interfaces.CALENDAR_UNIT_MINUTE,
		interfaces.CALENDAR_UNIT_HOUR,
		interfaces.CALENDAR_UNIT_DAY,
		interfaces.CALENDAR_UNIT_WEEK,
		interfaces.CALENDAR_UNIT_MONTH,
		interfaces.CALENDAR_UNIT_QUARTER,
		interfaces.CALENDAR_UNIT_YEAR:
		return nil
	default:
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_CalendarInterval).
			WithErrorDetails(fmt.Sprintf("Invalid calendar_interval value: %s, must be one of: minute, hour, day, week, month, quarter, year", calendarInterval))
	}
}

// validateAggregateParams verifies the aggregation query parameters
func validateAggregateParams(ctx context.Context, params *interfaces.ResourceDataQueryParams) error {
	// Verify aggregation
	if params.Aggregation != nil {
		if params.Aggregation.Property == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Aggregation).
				WithErrorDetails("Aggregation property cannot be empty")
		}
		// Verify the type of the aggregation function
		validAggr := map[string]bool{
			"count": true, "count_distinct": true, "sum": true,
			"max": true, "min": true, "avg": true,
		}
		if !validAggr[params.Aggregation.Aggr] {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Aggregation).
				WithErrorDetails(fmt.Sprintf("Unsupported aggregation function: %s", params.Aggregation.Aggr))
		}
	}

	// Verify group_by
	for _, groupByItem := range params.GroupBy {
		if groupByItem.Property == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_GroupBy).
				WithErrorDetails("GroupBy property cannot be empty")
		}
		// Verify calendar_interval
		if groupByItem.CalendarInterval != "" {
			err := validateCalendarInterval(ctx, groupByItem.CalendarInterval)
			if err != nil {
				return err
			}
		}
	}

	// Verify having
	if params.Having != nil {
		// having depends on aggregation or count(*)
		if params.Aggregation == nil && params.Having.Field != "count(*)" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Having).
				WithErrorDetails("Having clause requires aggregation or count(*)")
		}
		// Verify the field field
		if params.Having.Field != "__value" && params.Having.Field != "count(*)" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Having).
				WithErrorDetails("Having field must be '__value' or 'count(*)'")
		}
		// Verify operation
		validOps := map[string]bool{
			"==": true, "!=": true, ">": true, ">=": true,
			"<": true, "<=": true, "in": true, "not_in": true,
			"range": true, "out_range": true,
		}
		if !validOps[params.Having.Operation] {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Having).
				WithErrorDetails(fmt.Sprintf("Unsupported having operation: %s", params.Having.Operation))
		}
		// Validate the value.
		if params.Having.Value == nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Having).
				WithErrorDetails("Having value cannot be empty")
		}
	}

	return nil
}

// validateResourceDataQueryGroupByFields verifies that group-by fields are declared by the resource.
// It runs after the resource is loaded so request-level validation can remain resource independent.
func validateResourceDataQueryGroupByFields(ctx context.Context, params *interfaces.ResourceDataQueryParams,
	schemaDefinition []*interfaces.Property) error {
	fields := make(map[string]struct{}, len(schemaDefinition))
	for _, property := range schemaDefinition {
		if property != nil {
			fields[property.Name] = struct{}{}
		}
	}

	for _, groupByItem := range params.GroupBy {
		if _, ok := fields[groupByItem.Property]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_GroupBy).
				WithErrorDetails(fmt.Sprintf("GroupBy property %q is not defined by the resource", groupByItem.Property))
		}
	}

	return nil
}
