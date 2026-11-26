// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_target

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// Status values of a metric result and of its entries.
const (
	metricStatusOK          = "ok"
	metricStatusNoData      = "no_data"
	metricStatusUnavailable = "unavailable"
	metricStatusPartial     = "partial"
)

// byMetric compares the value of each group of a published metric version with the threshold.
// The groups are the objects of the bound object type, by the metric's statistical grain; an
// ungrouped metric is about the one object subject_key names. A group whose value is empty or not
// ok is not judged: the check does not cover it, so its event stays as it is.
func (s *Selector) byMetric(ctx context.Context, knID, branch string, metric *interfaces.ActionMetricTargets,
	subject *interfaces.ObjectType, clock CheckClock) (*Selection, error) {
	if s.Metrics == nil {
		return nil, selectionFailed("object metrics are not available in this edition")
	}
	if s.Queries != nil {
		if err := s.Queries.AuthorizeObjectMetricQuery(ctx, knID, branch, metric.MetricID); err != nil {
			return nil, selectionFailed("read metric %s: %s", metric.MetricID, DependencyReason(err))
		}
	}
	execution, err := s.Models.GetObjectMetricExecutionContext(ctx, knID, branch, metric.MetricID, metric.Version)
	if err != nil {
		return nil, selectionFailed("read metric %s version %d: %s", metric.MetricID, metric.Version, DependencyReason(err))
	}
	definition := execution.Root.Definition
	grain := definition.ResultSemantics.StatisticalGrain
	if err := s.checkMetricSubject(ctx, knID, branch, metric, grain, subject); err != nil {
		return nil, err
	}

	anchor := clock.At.UnixMilli()
	request := &interfaces.ObjectMetricQueryRequestV1{RuntimeAnchor: &anchor}
	if grain.TimeGranularity != "" {
		period, err := lastCompletePeriod(clock.At, grain)
		if err != nil {
			return nil, &SelectionError{Reason: fmt.Sprintf("metric %s: %v", metric.MetricID, err)}
		}
		request.Time = period
	}
	data, err := s.Metrics.QueryObjectMetricDataV1(ctx, knID, branch, metric.MetricID, metric.Version, request)
	if err != nil {
		return nil, selectionFailed("query metric %s version %d: %s", metric.MetricID, metric.Version, DependencyReason(err))
	}
	if data.Status == metricStatusUnavailable {
		return nil, selectionFailed("metric %s version %d is unavailable", metric.MetricID, metric.Version)
	}

	compare := eventMetricOperators[metric.Operator]
	unit := metricUnit(data.Model.Value.Unit)
	threshold := strconv.FormatFloat(metric.Value, 'f', -1, 64)
	// The entry each group is judged by: the latest bucket, when a time series returns several.
	latest := map[string]interfaces.ObjectMetricDataEntryV1{}
	order := []string{}
	unjudged := 0
	for _, entry := range data.Entries {
		key := metric.SubjectKey
		if len(grain.GroupByDimensions) > 0 {
			if len(entry.DimensionCells) == 0 {
				unjudged++
				continue
			}
			text, ok := interfaces.ScalarText(entry.DimensionCells[0].Value)
			if !ok || text == "" {
				unjudged++
				continue
			}
			key = text
		}
		previous, seen := latest[key]
		if !seen {
			order = append(order, key)
		}
		if !seen || entryTime(entry) >= entryTime(previous) {
			latest[key] = entry
		}
	}
	selection := &Selection{}
	covered := make([]string, 0, len(order))
	for _, key := range order {
		entry := latest[key]
		if entry.Status != "" && entry.Status != metricStatusOK || entry.Value == nil {
			unjudged++
			continue
		}
		value, err := common.AnyToFloat64(entry.Value)
		if err != nil {
			unjudged++
			continue
		}
		covered = append(covered, key)
		if !compare(value, metric.Value) {
			continue
		}
		start, end := metricPeriod(entry, data)
		valueText := interfaces.AttributeText(entry.Value)
		attributes := map[string]interfaces.EventAttribute{
			"value":          {Key: valueText, Label: valueText},
			"threshold":      {Key: threshold, Label: metric.Operator + " " + threshold},
			"metric_id":      {Key: metric.MetricID, Label: definition.Name},
			"metric_version": {Key: strconv.Itoa(metric.Version), Label: strconv.Itoa(metric.Version)},
		}
		detail := map[string]any{
			"metric_id": metric.MetricID, "version": metric.Version, "value": entry.Value,
			"operator": metric.Operator, "threshold": metric.Value,
		}
		if unit != "" {
			attributes["unit"] = interfaces.EventAttribute{Key: unit, Label: unit}
			detail["unit"] = unit
		}
		if start != nil {
			text := time.UnixMilli(*start).In(clock.Location).Format(time.RFC3339)
			attributes["period_start"] = interfaces.EventAttribute{Key: text, Label: text}
			detail["period_start"] = text
		}
		if end != nil {
			text := time.UnixMilli(*end).In(clock.Location).Format(time.RFC3339)
			attributes["period_end"] = interfaces.EventAttribute{Key: text, Label: text}
			detail["period_end"] = text
		}
		selection.Targets = append(selection.Targets, EventTarget{
			Identity: identityOfKey(subject, key),
			EventHit: interfaces.EventHit{Key: key, Attributes: attributes, Detail: map[string]any{"metric": detail}},
		})
	}
	switch {
	case unjudged > 0 || data.Status == metricStatusPartial:
		// Only the groups with a value were judged; the others keep their events as they are.
		selection.Covered = covered
		selection.Message = fmt.Sprintf("%d metric groups had no value to judge and were left as they are", unjudged)
	case len(data.Entries) == 0 && data.Status == metricStatusNoData:
		// No data is no answer: nothing recovers because the metric returned nothing.
		selection.Covered = []string{}
		selection.Message = "the metric returned no data"
	}
	return selection, nil
}

func entryTime(entry interfaces.ObjectMetricDataEntryV1) int64 {
	switch {
	case entry.TimeBucket != nil:
		return entry.TimeBucket.Start
	case entry.Time != nil:
		return *entry.Time
	}
	return 0
}

// metricPeriod is the period an entry's value was computed over: its bucket, else the result's
// effective window.
func metricPeriod(entry interfaces.ObjectMetricDataEntryV1, data interfaces.ObjectMetricDataV1) (*int64, *int64) {
	if entry.TimeBucket != nil {
		return &entry.TimeBucket.Start, &entry.TimeBucket.End
	}
	if data.EffectiveWindow != nil {
		return &data.EffectiveWindow.Start, &data.EffectiveWindow.End
	}
	return nil, nil
}

func metricUnit(unit *interfaces.ObjectMetricUnitSpecV1) string {
	if unit == nil {
		return ""
	}
	if unit.DisplaySymbol != "" {
		return unit.DisplaySymbol
	}
	return unit.Code
}

// checkMetricSubject checks that the metric's statistical grain leads to the bound object type:
// one group-by dimension that is its single primary key, or a property mapped to that key by
// exactly one direct relation; or no dimension, with subject_key naming the object.
func (s *Selector) checkMetricSubject(ctx context.Context, knID, branch string, metric *interfaces.ActionMetricTargets,
	grain interfaces.ObjectMetricStatisticalGrainV1, subject *interfaces.ObjectType) error {
	dimensions := grain.GroupByDimensions
	switch {
	case len(dimensions) == 0:
		if strings.TrimSpace(metric.SubjectKey) == "" {
			return selectionFailed("metric %s has no group-by: targets.metric.subject_key names the %s object it is about",
				metric.MetricID, subject.OTID)
		}
		return nil
	case len(dimensions) > 1:
		return selectionFailed("metric %s groups by more than one dimension; a record-event action takes one", metric.MetricID)
	case metric.SubjectKey != "":
		return selectionFailed("targets.metric.subject_key is only for a metric without group-by")
	}
	dimension := dimensions[0]
	if len(subject.PrimaryKeys) != 1 {
		return selectionFailed("a metric's groups name objects of %s only when it has a single primary key", subject.OTID)
	}
	primaryKey := subject.PrimaryKeys[0]
	if dimension.ObjectTypeID == subject.OTID && dimension.PropertyName == primaryKey {
		return nil
	}
	relations, err := s.Models.ListRelationTypes(ctx, knID, branch, interfaces.RelationTypesQuery{
		SourceObjectTypeIDs: []string{dimension.ObjectTypeID, subject.OTID},
		TargetObjectTypeIDs: []string{dimension.ObjectTypeID, subject.OTID},
	})
	if err != nil {
		return selectionFailed("read the relations of %s: %s", dimension.ObjectTypeID, DependencyReason(err))
	}
	matches := 0
	for _, relation := range relations {
		if relation.Type != "direct" {
			continue
		}
		var rules []interfaces.Mapping
		encoded, _ := json.Marshal(relation.MappingRules)
		if json.Unmarshal(encoded, &rules) != nil || len(rules) != 1 {
			continue
		}
		rule := rules[0]
		if relation.SourceObjectTypeID == dimension.ObjectTypeID && relation.TargetObjectTypeID == subject.OTID &&
			rule.SourceProp.Name == dimension.PropertyName && rule.TargetProp.Name == primaryKey ||
			relation.TargetObjectTypeID == dimension.ObjectTypeID && relation.SourceObjectTypeID == subject.OTID &&
				rule.TargetProp.Name == dimension.PropertyName && rule.SourceProp.Name == primaryKey {
			matches++
		}
	}
	switch matches {
	case 1:
		return nil
	case 0:
		return selectionFailed("metric %s groups by %s.%s, which does not lead to %s", metric.MetricID,
			dimension.ObjectTypeID, dimension.PropertyName, subject.OTID)
	}
	return selectionFailed("metric %s groups by %s.%s, which more than one relation maps to %s", metric.MetricID,
		dimension.ObjectTypeID, dimension.PropertyName, subject.OTID)
}

// lastCompletePeriod is the last whole period of the time series before at, in the grain's time
// zone, as inclusive milliseconds.
func lastCompletePeriod(at time.Time, grain interfaces.ObjectMetricStatisticalGrainV1) (*interfaces.ObjectMetricQueryTimeRangeV1, error) {
	location := time.UTC
	if zone := strings.TrimSpace(grain.Timezone); zone != "" {
		loaded, err := time.LoadLocation(zone)
		if err != nil {
			return nil, fmt.Errorf("time zone %q is not an IANA time zone", grain.Timezone)
		}
		location = loaded
	}
	local := at.In(location)
	var start, previous time.Time
	switch strings.ToLower(grain.TimeGranularity) {
	case common.CALENDAR_STEP_MINUTE:
		start = local.Truncate(time.Minute)
		previous = start.Add(-time.Minute)
	case common.CALENDAR_STEP_HOUR:
		start = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, location)
		previous = start.Add(-time.Hour)
	case common.CALENDAR_STEP_DAY:
		start = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
		previous = start.AddDate(0, 0, -1)
	case common.CALENDAR_STEP_WEEK:
		weekStart := time.Monday
		if strings.EqualFold(grain.WeekStart, "sunday") {
			weekStart = time.Sunday
		}
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
		start = day.AddDate(0, 0, -((int(day.Weekday()) - int(weekStart) + 7) % 7))
		previous = start.AddDate(0, 0, -7)
	case common.CALENDAR_STEP_MONTH:
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location)
		previous = start.AddDate(0, -1, 0)
	case common.CALENDAR_STEP_QUARTER:
		month := time.Month((int(local.Month())-1)/3*3 + 1)
		start = time.Date(local.Year(), month, 1, 0, 0, 0, 0, location)
		previous = start.AddDate(0, -3, 0)
	case common.CALENDAR_STEP_YEAR:
		start = time.Date(local.Year(), 1, 1, 0, 0, 0, 0, location)
		previous = start.AddDate(-1, 0, 0)
	default:
		return nil, fmt.Errorf("unsupported time granularity %q", grain.TimeGranularity)
	}
	return &interfaces.ObjectMetricQueryTimeRangeV1{Start: previous.UnixMilli(), End: start.UnixMilli() - 1}, nil
}
