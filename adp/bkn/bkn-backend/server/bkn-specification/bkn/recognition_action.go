// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A recognition action is the action type of operation type event (SPECIFICATION.md §8.9, from
// 2.1): it records the events of an event class whose source is rule. On its schedule, the
// platform selects the objects of the bound object type that meet its condition, or that its
// targets return, and records them as one check of the class. It calls no tool and takes no
// parameters.

// ActionOperationEvent is the operation type of a recognition action.
const ActionOperationEvent = "event"

// Schedule types.
const (
	ScheduleTypeCron    = "CRON"
	ScheduleTypeFixRate = "FIX_RATE"
)

const (
	// recognitionMinInterval is the shortest time between two checks.
	recognitionMinInterval   = 5 * time.Minute
	recognitionQueryMax      = 16 << 10
	recognitionParametersMax = 32
)

// ActionEventTargets selects the objects of a recognition action that does not select them by its
// condition: exactly one of a Cypher query, a function, or a metric anomaly.
type ActionEventTargets struct {
	Cypher   *ActionCypherTargets   `yaml:"cypher,omitempty"`
	Function *ActionFunctionTargets `yaml:"function,omitempty"`
	Metric   *ActionMetricTargets   `yaml:"metric,omitempty"`
}

// ActionTargetParameter is a parameter of a target query or function: a constant, or the check
// time or date moved by an ISO 8601 duration.
type ActionTargetParameter struct {
	ValueFrom string `yaml:"value_from"`
	Value     any    `yaml:"value,omitempty"`
}

// ActionCypherTargets selects the objects a read-only Cypher query of the network returns.
type ActionCypherTargets struct {
	Query      string                           `yaml:"query"`
	Parameters map[string]ActionTargetParameter `yaml:"parameters,omitempty"`
}

// ActionFunctionTargets selects the objects a Cypher function of the network returns.
type ActionFunctionTargets struct {
	BoxID      string                           `yaml:"box_id"`
	ToolID     string                           `yaml:"tool_id"`
	Parameters map[string]ActionTargetParameter `yaml:"parameters,omitempty"`
}

// ActionMetricTargets selects the groups of a published metric version whose value meets the
// threshold. SubjectKey is the object a metric without groups is about.
type ActionMetricTargets struct {
	MetricID   string  `yaml:"metric_id"`
	Version    int     `yaml:"version"`
	Operator   string  `yaml:"operator"`
	Value      float64 `yaml:"value"`
	SubjectKey string  `yaml:"subject_key,omitempty"`
}

// IsRecognitionAction reports whether the action type records the events of an event class.
func (at *BknActionType) IsRecognitionAction() bool {
	return at != nil && at.OperationConfig != nil && at.OperationConfig.Type == ActionOperationEvent
}

// checkActionOperationSpec refuses what an action type's Operation Config cannot hold under the
// package's specification: a recognition action comes with 2.1, like the event classes it records.
func checkActionOperationSpec(at *BknActionType, opts parseOptions) error {
	cfg := at.OperationConfig
	if cfg == nil {
		return nil
	}
	recognition := cfg.Type == ActionOperationEvent
	if !opts.eventClasses() && (recognition || cfg.EventClass != "" || cfg.Targets != nil) {
		return specErrorf("action type %s: operation_config.type event records the events of an event class, which come with bkn_spec 2.1; declare bkn_spec: \"2.1\" in network.bkn", at.ID)
	}
	if !recognition && (cfg.EventClass != "" || cfg.Targets != nil) {
		return specErrorf("action type %s: event_class and targets belong to operation_config.type event", at.ID)
	}
	return nil
}

// withoutRecognitionActions is the network without its recognition actions, which a 2.0 package
// cannot hold. The network itself is not changed.
func withoutRecognitionActions(doc *BknNetwork) *BknNetwork {
	kept := make([]*BknActionType, 0, len(doc.ActionTypes))
	for _, at := range doc.ActionTypes {
		if !at.IsRecognitionAction() {
			kept = append(kept, at)
		}
	}
	if len(kept) == len(doc.ActionTypes) {
		return doc
	}
	copied := *doc
	copied.ActionTypes = kept
	return &copied
}

var (
	targetParameterName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	fixRateExpression   = regexp.MustCompile(`^([1-9][0-9]{0,5})([mhd])$`)
	metricOperators     = map[string]bool{"<": true, "<=": true, ">": true, ">=": true, "==": true, "!=": true}
)

// validateRecognitionAction checks a recognition action within its network. classes are the
// network's object types by ID; recorded maps each event class to the first recognition action
// that records it.
func validateRecognitionAction(result *ValidationResult, table string, at *BknActionType,
	objects map[string]*BknObjectType, recorded map[string]string) {
	cfg := at.OperationConfig
	classID := strings.TrimSpace(cfg.EventClass)
	bound := strings.TrimSpace(at.BoundObject)
	switch class := objects[classID]; {
	case classID == "":
		appendError(result, table, "operation_config.event_class", "invalid_recognition_action",
			"operation_config.event_class is required for operation_config.type event: the event class the action records")
	case class == nil || !class.IsEventClass() || class.Event == nil:
		appendError(result, table, "operation_config.event_class", "invalid_event_class_ref",
			fmt.Sprintf("event class %q is not an event class of the network", classID))
	case class.Event.Source != EventSourceRule:
		appendError(result, table, "operation_config.event_class", "event_class_not_rule",
			fmt.Sprintf("the events of class %q are reported: only a class whose source is rule has a recognition action", classID))
	case class.Event.Subject != bound:
		appendError(result, table, "Bound Object", "event_subject_mismatch",
			fmt.Sprintf("event class %q is about %q; the recognition action must be bound to it, not to %q", classID, class.Event.Subject, bound))
	}
	if classID != "" {
		if first, ok := recorded[classID]; ok {
			appendError(result, table, "operation_config.event_class", "duplicate_recognition_action",
				fmt.Sprintf("event class %q already has the recognition action %q: a class has one", classID, first))
		} else {
			recorded[classID] = at.ID
		}
	}
	if bound == "" {
		appendError(result, table, "Bound Object", "invalid_recognition_action",
			"a recognition action is bound to the object type its events are about")
	}
	for _, kind := range []string{at.ActionType, at.ActionIntent} {
		if kind = strings.TrimSpace(kind); kind != "" && !strings.EqualFold(kind, "add") {
			appendError(result, table, "action_intent", "invalid_recognition_action",
				fmt.Sprintf("a recognition action records events: its action_intent is add, not %q", kind))
			break
		}
	}
	if at.ActionSource != nil && strings.TrimSpace(at.ActionSource.Type) != "" {
		appendError(result, table, "Action Source", "recognition_action_source",
			"a recognition action calls no tool: the platform records its check; leave Action Source empty")
	}
	if len(at.Parameters) > 0 {
		appendError(result, table, "Parameter Binding", "recognition_action_source",
			"a recognition action takes no parameters; leave Parameter Binding empty")
	}
	if err := checkRecognitionBasis(at); err != nil {
		appendError(result, table, "operation_config.targets", "invalid_recognition_basis", err.Error())
	}
	if err := checkRecognitionSchedule(at.Schedule); err != nil {
		appendError(result, table, "Schedule", "invalid_recognition_schedule", err.Error())
	}
}

// checkRecognitionBasis checks what a recognition action selects its objects by: its condition,
// or exactly one kind of targets.
func checkRecognitionBasis(at *BknActionType) error {
	targets := at.OperationConfig.Targets
	kinds := 0
	if targets != nil {
		for _, set := range []bool{targets.Cypher != nil, targets.Function != nil, targets.Metric != nil} {
			if set {
				kinds++
			}
		}
		if kinds != 1 {
			return fmt.Errorf("operation_config.targets holds exactly one of cypher, function and metric")
		}
	}
	switch {
	case kinds == 0 && at.TriggerCondition == nil:
		return fmt.Errorf("a recognition action without targets selects its objects by its Trigger Condition, which is required")
	case kinds == 1 && at.TriggerCondition != nil:
		return fmt.Errorf("a recognition action with targets has no Trigger Condition")
	case kinds == 0:
		return checkRecognitionCondition(at.TriggerCondition)
	case targets.Cypher != nil:
		query := strings.TrimSpace(targets.Cypher.Query)
		if query == "" {
			return fmt.Errorf("targets.cypher.query is required")
		}
		if len(query) > recognitionQueryMax {
			return fmt.Errorf("targets.cypher.query exceeds %d bytes", recognitionQueryMax)
		}
		return checkTargetParameters("targets.cypher", targets.Cypher.Parameters, true)
	case targets.Function != nil:
		if strings.TrimSpace(targets.Function.BoxID) == "" || strings.TrimSpace(targets.Function.ToolID) == "" {
			return fmt.Errorf("targets.function requires box_id and tool_id")
		}
		return checkTargetParameters("targets.function", targets.Function.Parameters, false)
	default:
		metric := targets.Metric
		switch {
		case strings.TrimSpace(metric.MetricID) == "":
			return fmt.Errorf("targets.metric.metric_id is required")
		case metric.Version < 1:
			return fmt.Errorf("targets.metric.version is a published version of the metric")
		case !metricOperators[metric.Operator]:
			return fmt.Errorf("targets.metric.operator must be one of <, <=, >, >=, ==, !=")
		case math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0):
			return fmt.Errorf("targets.metric.value must be a number")
		}
		return nil
	}
}

func checkTargetParameters(where string, parameters map[string]ActionTargetParameter, builtins bool) error {
	if len(parameters) > recognitionParametersMax {
		return fmt.Errorf("%s takes at most %d parameters", where, recognitionParametersMax)
	}
	for name, parameter := range parameters {
		if !targetParameterName.MatchString(name) {
			return fmt.Errorf("%s parameter name %q must be letters, digits and _", where, name)
		}
		if builtins && (name == ValueFromCheckTime || name == ValueFromCheckDate) {
			return fmt.Errorf("%s parameter %q is given by the platform", where, name)
		}
		switch parameter.ValueFrom {
		case "", "const":
		case ValueFromCheckTime, ValueFromCheckDate:
			if err := checkOffsetValue(parameter.ValueFrom, parameter.Value); err != nil {
				return fmt.Errorf("%s parameter %q: %w", where, name, err)
			}
		default:
			return fmt.Errorf("%s parameter %q takes value_from const, check_time or check_date, not %q", where, name, parameter.ValueFrom)
		}
	}
	return nil
}

// checkRecognitionCondition allows a recognition action's condition the values a scheduled check
// has: constants, the object's own properties, and the check time and date.
func checkRecognitionCondition(cfg *ActionCondCfg) error {
	if cfg == nil {
		return nil
	}
	for _, sub := range cfg.SubConds {
		if err := checkRecognitionCondition(sub); err != nil {
			return err
		}
	}
	switch valueFrom := strings.TrimSpace(cfg.ValueFrom); valueFrom {
	case "", "const", valueFromProperty:
		return nil
	case ValueFromCheckTime, ValueFromCheckDate:
		if err := checkOffsetValue(valueFrom, cfg.Value); err != nil {
			return fmt.Errorf("condition on %s: %w", cfg.Field, err)
		}
		return nil
	default:
		return fmt.Errorf("a recognition action's condition takes value_from const, property, check_time or check_date, not %q", cfg.ValueFrom)
	}
}

// conditionTakesCheckTime reports whether a condition compares with the check time or date, which
// only a recognition action has.
func conditionTakesCheckTime(cfg *ActionCondCfg) bool {
	if cfg == nil {
		return false
	}
	switch strings.TrimSpace(cfg.ValueFrom) {
	case ValueFromCheckTime, ValueFromCheckDate:
		return true
	}
	for _, sub := range cfg.SubConds {
		if conditionTakesCheckTime(sub) {
			return true
		}
	}
	return false
}

func checkOffsetValue(valueFrom string, value any) error {
	if value == nil {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s takes an ISO 8601 duration such as -PT48H, not %v", valueFrom, value)
	}
	_, err := ParseCheckOffset(text, valueFrom == ValueFromCheckDate)
	return err
}

// checkRecognitionSchedule checks when a recognition action runs: a five-field cron expression or
// a fixed rate such as 30m, 2h or 1d, no more often than every five minutes, in an IANA time zone.
// The platform also checks that a cron expression runs no more often than that.
func checkRecognitionSchedule(schedule *Schedule) error {
	if schedule == nil || strings.TrimSpace(schedule.Type) == "" {
		return fmt.Errorf("a recognition action runs on its Schedule, which is required")
	}
	zone := strings.TrimSpace(schedule.Timezone)
	if zone == "" || zone == "Local" {
		return fmt.Errorf("the Schedule of a recognition action needs its Timezone, an IANA time zone such as Asia/Shanghai")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return fmt.Errorf("schedule timezone %q is not an IANA time zone", zone)
	}
	expression := strings.TrimSpace(schedule.Expression)
	switch strings.TrimSpace(schedule.Type) {
	case ScheduleTypeFixRate:
		m := fixRateExpression.FindStringSubmatch(expression)
		if m == nil {
			return fmt.Errorf("a FIX_RATE expression is a number and m, h or d, such as 30m")
		}
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
		if time.Duration(n)*unit < recognitionMinInterval {
			return fmt.Errorf("a recognition action runs at most every %s", recognitionMinInterval)
		}
	case ScheduleTypeCron:
		if strings.HasPrefix(expression, "TZ=") || strings.HasPrefix(expression, "CRON_TZ=") {
			return fmt.Errorf("write the time zone in the Timezone column, not in the cron expression")
		}
		if len(strings.Fields(expression)) != 5 {
			return fmt.Errorf("a CRON expression has five fields: minute, hour, day of month, month, day of week")
		}
	default:
		return fmt.Errorf("schedule type must be %s or %s, got %q", ScheduleTypeCron, ScheduleTypeFixRate, schedule.Type)
	}
	return nil
}
