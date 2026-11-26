// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_target

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// EventActionsLicensed reports whether record-event actions may run: they come with the
// Professional edition. Without it the operation category is unknown, as it is to an older
// version. A variable so tests can set the edition.
var EventActionsLicensed = func() bool { return entitlement.AtLeast(licverify.EditionProfessional) }

// Schedule types of a record-event action, as bkn-backend stores them.
const (
	eventScheduleCron    = "CRON"
	eventScheduleFixRate = "FIX_RATE"
)

// Metric operators of a record-event action.
var eventMetricOperators = map[string]func(value, threshold float64) bool{
	"<":  func(v, t float64) bool { return v < t },
	"<=": func(v, t float64) bool { return v <= t },
	">":  func(v, t float64) bool { return v > t },
	">=": func(v, t float64) bool { return v >= t },
	"==": func(v, t float64) bool { return v == t },
	"!=": func(v, t float64) bool { return v != t },
}

// validateEventDefinition checks what a record-event action is made of, without reading the
// network: the event class, its subject, its basis (a condition or exactly one target source),
// the schedule's time zone, and the value sources its condition and parameters use.
func validateEventDefinition(action *interfaces.ActionType) error {
	cfg := action.OperationConfig
	switch {
	case strings.TrimSpace(action.ObjectTypeID) == "":
		return fmt.Errorf("a record-event action binds the object type its events are about")
	case strings.TrimSpace(cfg.EventClass) == "":
		return fmt.Errorf("a record-event action requires operation_config.event_class")
	case action.ActionSource.Type != "" || action.ActionSource.BoxID != "" || action.ActionSource.ToolID != "" || action.ActionSource.McpID != "":
		return fmt.Errorf("a record-event action has no action source: it records its hits in the event ledger itself")
	case len(action.Parameters) != 0:
		return fmt.Errorf("a record-event action has no parameters")
	}
	if _, err := eventLocation(action); err != nil {
		return err
	}
	if cfg.Targets == nil {
		if action.Condition == nil {
			return fmt.Errorf("a record-event action selects its objects by its condition or by operation_config.targets")
		}
		return validateEventCondition(action.Condition, action.ObjectTypeID)
	}
	if action.Condition != nil {
		return fmt.Errorf("a record-event action with operation_config.targets has no condition")
	}
	targets := cfg.Targets
	count := 0
	for _, set := range []bool{targets.Cypher != nil, targets.Function != nil, targets.Metric != nil} {
		if set {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("operation_config.targets holds exactly one of cypher, function and metric")
	}
	switch {
	case targets.Cypher != nil:
		if strings.TrimSpace(targets.Cypher.Query) == "" {
			return fmt.Errorf("targets.cypher.query is required")
		}
		for name, parameter := range targets.Cypher.Parameters {
			if name == interfaces.ActionValueFromCheckTime || name == interfaces.ActionValueFromCheckDate {
				return fmt.Errorf("targets.cypher.parameters.%s: $%s is given by the platform", name, name)
			}
			if err := validateTargetParameter("targets.cypher.parameters."+name, parameter); err != nil {
				return err
			}
		}
	case targets.Function != nil:
		if strings.TrimSpace(targets.Function.BoxID) == "" || strings.TrimSpace(targets.Function.ToolID) == "" {
			return fmt.Errorf("targets.function requires box_id and tool_id")
		}
		for name, parameter := range targets.Function.Parameters {
			if err := validateTargetParameter("targets.function.parameters."+name, parameter); err != nil {
				return err
			}
		}
	default:
		metric := targets.Metric
		if strings.TrimSpace(metric.MetricID) == "" || metric.Version < 1 {
			return fmt.Errorf("targets.metric requires metric_id and a published version")
		}
		if _, ok := eventMetricOperators[metric.Operator]; !ok {
			return fmt.Errorf("targets.metric.operator must be one of <, <=, >, >=, == and !=")
		}
	}
	return nil
}

// eventLocation is the time zone of a record-event action's checks: its schedule's.
func eventLocation(action *interfaces.ActionType) (*time.Location, error) {
	schedule := action.Schedule
	if schedule.Type != eventScheduleCron && schedule.Type != eventScheduleFixRate {
		return nil, fmt.Errorf("a record-event action checks on its schedule: schedule.type must be CRON or FIX_RATE")
	}
	zone := strings.TrimSpace(schedule.Timezone)
	if zone == "" || zone == "Local" {
		return nil, fmt.Errorf("a record-event action requires schedule.timezone, an IANA time zone")
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("schedule.timezone %q is not an IANA time zone", schedule.Timezone)
	}
	return location, nil
}

// validateEventCondition allows a recognition condition only constants, another property of the
// same object, and the check time or day: a check has no input and no current user.
func validateEventCondition(cfg *cond.CondCfg, objectTypeID string) error {
	if cfg == nil {
		return fmt.Errorf("null condition child")
	}
	if cfg.ObjectTypeID != "" && cfg.ObjectTypeID != objectTypeID {
		return fmt.Errorf("the condition of a record-event action reads its bound object type only")
	}
	if cfg.Operation == cond.OperationAnd || cfg.Operation == cond.OperationOr {
		if len(cfg.SubConds) == 0 {
			return fmt.Errorf("logical conditions require children")
		}
		for _, child := range cfg.SubConds {
			if err := validateEventCondition(child, objectTypeID); err != nil {
				return err
			}
		}
		return nil
	}
	switch cfg.ValueFrom {
	case "", cond.ValueFrom_Const, interfaces.LOGIC_PARAMS_VALUE_FROM_PROP:
		return nil
	case interfaces.ActionValueFromCheckTime, interfaces.ActionValueFromCheckDate:
		_, err := parseCheckShift(cfg.Value)
		if err != nil {
			return fmt.Errorf("condition on %q: %w", cfg.Name, err)
		}
		return nil
	}
	return fmt.Errorf("condition on %q: a record-event action's condition takes const, property, check_time or check_date, not %q",
		cfg.Name, cfg.ValueFrom)
}

func validateTargetParameter(path string, parameter interfaces.ActionTargetParameter) error {
	switch parameter.ValueFrom {
	case cond.ValueFrom_Const:
		if parameter.Value == nil {
			return fmt.Errorf("%s has no value", path)
		}
		return nil
	case interfaces.ActionValueFromCheckTime, interfaces.ActionValueFromCheckDate:
		if _, err := parseCheckShift(parameter.Value); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	return fmt.Errorf("%s: value_from must be const, check_time or check_date", path)
}

// CheckClock is the time of one check and the time zone its day is counted in.
type CheckClock struct {
	At       time.Time
	Location *time.Location
}

// EventCheckClock is the clock of a check of a record-event action at at: its schedule's zone.
func EventCheckClock(action *interfaces.ActionType, at time.Time) (CheckClock, error) {
	location, err := eventLocation(action)
	if err != nil {
		return CheckClock{}, err
	}
	return CheckClock{At: at, Location: location}, nil
}

// checkShift is an ISO 8601 duration: calendar years, months and days, and a clock duration.
type checkShift struct {
	years, months, days int
	clock               time.Duration
}

var isoDurationPattern = regexp.MustCompile(`^([+-])?P(?:(\d+)Y)?(?:(\d+)M)?(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?)?$`)

// parseCheckShift reads the optional ISO 8601 duration of a check_time or check_date value,
// such as -PT48H or -P3D. No value is no shift.
func parseCheckShift(value any) (checkShift, error) {
	if value == nil {
		return checkShift{}, nil
	}
	text, ok := value.(string)
	if !ok {
		return checkShift{}, fmt.Errorf("the value of check_time or check_date is an ISO 8601 duration such as -PT48H")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return checkShift{}, nil
	}
	match := isoDurationPattern.FindStringSubmatch(strings.ToUpper(text))
	if match == nil || strings.HasSuffix(strings.ToUpper(text), "T") || strings.TrimLeft(strings.ToUpper(text), "+-") == "P" {
		return checkShift{}, fmt.Errorf("%q is not an ISO 8601 duration such as -PT48H or -P3D", text)
	}
	sign := 1
	if match[1] == "-" {
		sign = -1
	}
	number := func(index int) int {
		if match[index] == "" {
			return 0
		}
		n, _ := strconv.Atoi(match[index])
		return n
	}
	shift := checkShift{years: sign * number(2), months: sign * number(3), days: sign * (7*number(4) + number(5))}
	shift.clock = time.Duration(number(6))*time.Hour + time.Duration(number(7))*time.Minute
	if match[8] != "" {
		seconds, err := strconv.ParseFloat(match[8], 64)
		if err != nil {
			return checkShift{}, fmt.Errorf("%q is not an ISO 8601 duration", text)
		}
		shift.clock += time.Duration(seconds * float64(time.Second))
	}
	shift.clock *= time.Duration(sign)
	return shift, nil
}

func (s checkShift) apply(t time.Time) time.Time {
	return t.AddDate(s.years, s.months, s.days).Add(s.clock)
}

// Value is what a value source stands for at this check: check_time is the check time plus the
// duration, in RFC 3339 in the check's time zone; check_date is the check day there plus the
// duration, as YYYY-MM-DD. Any other source keeps its value.
func (c CheckClock) Value(valueFrom string, value any) (any, error) {
	switch valueFrom {
	case interfaces.ActionValueFromCheckTime, interfaces.ActionValueFromCheckDate:
	default:
		return value, nil
	}
	shift, err := parseCheckShift(value)
	if err != nil {
		return nil, err
	}
	local := c.At.In(c.Location)
	if valueFrom == interfaces.ActionValueFromCheckTime {
		return shift.apply(local).Format(time.RFC3339), nil
	}
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, c.Location)
	return shift.apply(day).Format(time.DateOnly), nil
}

// parameters resolves a Cypher query's or a function's parameters at this check.
func (c CheckClock) parameters(parameters map[string]interfaces.ActionTargetParameter) (map[string]any, error) {
	values := make(map[string]any, len(parameters)+2)
	for name, parameter := range parameters {
		value, err := c.Value(parameter.ValueFrom, parameter.Value)
		if err != nil {
			return nil, fmt.Errorf("parameter %s: %w", name, err)
		}
		values[name] = value
	}
	return values, nil
}
