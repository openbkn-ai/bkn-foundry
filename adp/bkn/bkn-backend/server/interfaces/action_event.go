// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	bkn "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
)

// A record-event action type (operation_config.type event) is the recognition action of an event
// class whose source is rule. On its schedule, ontology-query selects the objects of the bound
// object type that meet its condition, or that its targets return, and records them as the class's
// check in the event ledger. It writes nothing else: it has no action source and no parameters.
const (
	ActionOperationEvent = "event"

	ActionScheduleCron    = "CRON"
	ActionScheduleFixRate = "FIX_RATE"

	// ActionEventMinInterval is the shortest time between two checks.
	ActionEventMinInterval = 5 * time.Minute

	actionCypherParameterMax = 32
	actionCypherQueryMax     = 16 << 10
)

// ActionEventTargets is the basis of a recognition action that is not its condition: exactly one
// of a Cypher query, a function, or a metric anomaly.
type ActionEventTargets struct {
	Cypher   *ActionCypherTargets   `json:"cypher,omitempty" mapstructure:"cypher"`
	Function *ActionFunctionTargets `json:"function,omitzero" mapstructure:"function"`
	Metric   *ActionMetricTargets   `json:"metric,omitempty" mapstructure:"metric"`
}

// ActionTargetParameter is a parameter of a target query or function: a constant, or the check
// time or date moved by an ISO 8601 duration.
type ActionTargetParameter struct {
	ValueFrom string `json:"value_from" mapstructure:"value_from"`
	Value     any    `json:"value,omitempty" mapstructure:"value"`
}

// ActionCypherTargets selects the objects a read-only Cypher query of the network returns.
type ActionCypherTargets struct {
	Query      string                           `json:"query" mapstructure:"query"`
	Parameters map[string]ActionTargetParameter `json:"parameters,omitempty" mapstructure:"parameters"`
}

// ActionFunctionTargets selects the objects a Cypher function of the network returns as a table.
type ActionFunctionTargets struct {
	BoxID      string                           `json:"box_id" mapstructure:"box_id"`
	ToolID     string                           `json:"tool_id" mapstructure:"tool_id"`
	Parameters map[string]ActionTargetParameter `json:"parameters,omitempty" mapstructure:"parameters"`
}

// ActionMetricTargets selects the groups of a published metric version whose value meets the
// threshold. SubjectKey is the object an aggregate metric, one without groups, is about.
type ActionMetricTargets struct {
	MetricID   string  `json:"metric_id" mapstructure:"metric_id"`
	Version    int     `json:"version" mapstructure:"version"`
	Operator   string  `json:"operator" mapstructure:"operator"`
	Value      float64 `json:"value" mapstructure:"value"`
	SubjectKey string  `json:"subject_key,omitempty" mapstructure:"subject_key"`
}

// ActionEventTrial is the trial run of a recognition action: the objects it selects now.
type ActionEventTrial struct {
	Subject          string           `json:"subject"`
	Hits             []map[string]any `json:"hits"`
	HitCount         int              `json:"hit_count"`
	Limited          bool             `json:"limited"`
	RowFilterApplied *bool            `json:"row_filter_applied,omitzero"`
}

var metricOperators = map[string]bool{"<": true, "<=": true, ">": true, ">=": true, "==": true, "!=": true}

// IsEventAction reports whether the action type is a recognition action.
func IsEventAction(at *ActionType) bool {
	return at != nil && at.OperationConfig != nil && at.OperationConfig.Type == ActionOperationEvent
}

// EventClassOf is the event class a recognition action records, or "".
func EventClassOf(at *ActionType) string {
	if !IsEventAction(at) {
		return ""
	}
	return at.OperationConfig.EventClass
}

// validateEventOperation checks a recognition action on its own. Whether the event class exists,
// is about the bound object type and has no other recognition action is the event module's check.
func validateEventOperation(at *ActionType) error {
	cfg := at.OperationConfig
	if at.ObjectTypeID == "" {
		return fmt.Errorf("operation_config.type=event requires object_type_id: the object type the events are about")
	}
	if at.ActionType != ACTION_TYPE_ADD {
		return fmt.Errorf("a record-event action type records events: its action_type is add")
	}
	if strings.TrimSpace(cfg.EventClass) == "" {
		return fmt.Errorf("operation_config.event_class is required for operation_config.type=event")
	}
	if cfg.RelationTypeID != "" || cfg.RelationOperation != "" || cfg.AnchorRole != "" || cfg.OtherObjectCondition != nil {
		return fmt.Errorf("relation fields are only valid for relation operations")
	}
	if at.ActionSource != (ActionSource{}) {
		return fmt.Errorf("a record-event action type has no action_source: the platform records its check")
	}
	if len(at.Parameters) > 0 {
		return fmt.Errorf("a record-event action type has no parameters")
	}
	if _, err := ParseEventSchedule(at.Schedule); err != nil {
		return err
	}
	bases := 0
	if cfg.Targets != nil {
		if cfg.Targets.Cypher != nil {
			bases++
		}
		if cfg.Targets.Function != nil {
			bases++
		}
		if cfg.Targets.Metric != nil {
			bases++
		}
		if bases != 1 {
			return fmt.Errorf("operation_config.targets holds exactly one of cypher, function and metric")
		}
	}
	switch {
	case bases == 0 && at.Condition == nil:
		return fmt.Errorf("a record-event action type without targets selects its objects by its condition, which is required")
	case bases == 1 && at.Condition != nil:
		return fmt.Errorf("a record-event action type with targets has no condition")
	}
	if err := validateEventCondition(at.Condition, at.ObjectTypeID); err != nil {
		return err
	}
	if cfg.Targets == nil {
		return nil
	}
	switch {
	case cfg.Targets.Cypher != nil:
		return validateCypherTargets(cfg.Targets.Cypher)
	case cfg.Targets.Function != nil:
		return validateFunctionTargets(cfg.Targets.Function)
	default:
		return validateMetricTargets(cfg.Targets.Metric)
	}
}

// validateEventCondition allows a recognition action's condition the values it can take on a
// schedule: constants, the object's own properties, and the check time and date. It has no input,
// and the current user of a scheduled check would be its execution subject.
func validateEventCondition(cfg *ActionCondCfg, objectTypeID string) error {
	if cfg == nil {
		return nil
	}
	if cfg.ObjectTypeID != "" && cfg.ObjectTypeID != objectTypeID {
		return fmt.Errorf("condition references an unrelated object type %q", cfg.ObjectTypeID)
	}
	if len(cfg.SubConds) > 0 {
		for _, sub := range cfg.SubConds {
			if sub == nil {
				return fmt.Errorf("condition contains a null subcondition")
			}
			if err := validateEventCondition(sub, objectTypeID); err != nil {
				return err
			}
		}
		return nil
	}
	switch cfg.ValueFrom {
	case "", "const":
		return nil
	case "property":
		if name, ok := cfg.Value.(string); !ok || name == "" {
			return fmt.Errorf("condition property reference must be a nonempty name")
		}
	case bkn.ValueFromCheckTime, bkn.ValueFromCheckDate:
		if err := checkOffset(cfg.ValueFrom, cfg.Value); err != nil {
			return fmt.Errorf("condition on %s: %w", cfg.Field, err)
		}
	default:
		return fmt.Errorf("a record-event condition takes const, property, check_time or check_date, not %q", cfg.ValueFrom)
	}
	return nil
}

func checkOffset(valueFrom string, value any) error {
	if value == nil {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s takes an ISO 8601 duration such as -PT48H, not %v", valueFrom, value)
	}
	_, err := bkn.ParseCheckOffset(text, valueFrom == bkn.ValueFromCheckDate)
	return err
}

var targetParameterName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func validateTargetParameters(parameters map[string]ActionTargetParameter, builtins bool) error {
	if len(parameters) > actionCypherParameterMax {
		return fmt.Errorf("targets take at most %d parameters", actionCypherParameterMax)
	}
	for name, parameter := range parameters {
		if !targetParameterName.MatchString(name) {
			return fmt.Errorf("target parameter name %q must be letters, digits and _", name)
		}
		if builtins && (name == bkn.ValueFromCheckTime || name == bkn.ValueFromCheckDate) {
			return fmt.Errorf("target parameter %q is given by the platform", name)
		}
		switch parameter.ValueFrom {
		case "", "const":
		case bkn.ValueFromCheckTime, bkn.ValueFromCheckDate:
			if err := checkOffset(parameter.ValueFrom, parameter.Value); err != nil {
				return fmt.Errorf("target parameter %q: %w", name, err)
			}
		default:
			return fmt.Errorf("target parameter %q takes const, check_time or check_date, not %q", name, parameter.ValueFrom)
		}
	}
	return nil
}

func validateCypherTargets(targets *ActionCypherTargets) error {
	query := strings.TrimSpace(targets.Query)
	if query == "" {
		return fmt.Errorf("targets.cypher.query is required")
	}
	if len(query) > actionCypherQueryMax {
		return fmt.Errorf("targets.cypher.query exceeds %d bytes", actionCypherQueryMax)
	}
	return validateTargetParameters(targets.Parameters, true)
}

func validateFunctionTargets(targets *ActionFunctionTargets) error {
	if strings.TrimSpace(targets.BoxID) == "" || strings.TrimSpace(targets.ToolID) == "" {
		return fmt.Errorf("targets.function requires box_id and tool_id")
	}
	return validateTargetParameters(targets.Parameters, false)
}

func validateMetricTargets(targets *ActionMetricTargets) error {
	switch {
	case strings.TrimSpace(targets.MetricID) == "":
		return fmt.Errorf("targets.metric.metric_id is required")
	case targets.Version < 1:
		return fmt.Errorf("targets.metric.version is a published version of the metric")
	case !metricOperators[targets.Operator]:
		return fmt.Errorf("targets.metric.operator must be one of <, <=, >, >=, ==, !=")
	case math.IsNaN(targets.Value) || math.IsInf(targets.Value, 0):
		return fmt.Errorf("targets.metric.value must be a number")
	}
	return nil
}

// EventSchedule is the schedule of a recognition action, read.
type EventSchedule struct {
	cron.Schedule
	// Expression is the schedule as the action schedule worker reads it: CRON_TZ=<zone> <cron>,
	// or @every <interval>.
	Expression string
}

var fixRate = regexp.MustCompile(`^([1-9][0-9]{0,5})([mhd])$`)

// ParseEventSchedule reads the schedule of a recognition action: a five-field cron in its time
// zone, or a fixed rate such as 30m, 2h or 1d, no more often than every five minutes.
func ParseEventSchedule(schedule Schedule) (EventSchedule, error) {
	zone := strings.TrimSpace(schedule.Timezone)
	if zone == "" || zone == "Local" {
		return EventSchedule{}, fmt.Errorf("a record-event schedule requires timezone, an IANA time zone such as Asia/Shanghai")
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return EventSchedule{}, fmt.Errorf("schedule.timezone %q is not an IANA time zone", zone)
	}
	expression := strings.TrimSpace(schedule.Expression)
	switch schedule.Type {
	case ActionScheduleFixRate:
		m := fixRate.FindStringSubmatch(expression)
		if m == nil {
			return EventSchedule{}, fmt.Errorf("a FIX_RATE expression is a number and m, h or d, such as 30m")
		}
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
		every := time.Duration(n) * unit
		if every < ActionEventMinInterval {
			return EventSchedule{}, fmt.Errorf("a record-event schedule runs at most every %s", ActionEventMinInterval)
		}
		return EventSchedule{Schedule: cron.Every(every), Expression: "@every " + every.String()}, nil
	case ActionScheduleCron:
		if strings.HasPrefix(expression, "TZ=") || strings.HasPrefix(expression, "CRON_TZ=") {
			return EventSchedule{}, fmt.Errorf("write the time zone in schedule.timezone, not in the cron expression")
		}
		parsed, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(expression)
		if err != nil {
			return EventSchedule{}, fmt.Errorf("schedule.expression is not a five-field cron expression: %v", err)
		}
		if err := minimumInterval(parsed, location); err != nil {
			return EventSchedule{}, err
		}
		worker := "CRON_TZ=" + zone + " " + expression
		zoned, err := ScheduleParser().Parse(worker)
		if err != nil {
			return EventSchedule{}, fmt.Errorf("schedule.expression is not a five-field cron expression: %v", err)
		}
		return EventSchedule{Schedule: zoned, Expression: worker}, nil
	default:
		return EventSchedule{}, fmt.Errorf("schedule.type must be CRON or FIX_RATE")
	}
}

// minimumInterval probes the next runs of a cron schedule for two closer than the minimum.
func minimumInterval(schedule cron.Schedule, location *time.Location) error {
	at := time.Now().In(location)
	previous := schedule.Next(at)
	for range 2000 {
		next := schedule.Next(previous)
		if next.IsZero() {
			return nil
		}
		if next.Sub(previous) < ActionEventMinInterval {
			return fmt.Errorf("a record-event schedule runs at most every %s", ActionEventMinInterval)
		}
		previous = next
	}
	return nil
}

// scheduleParser reads every expression the action schedule worker runs: the five-field cron of a
// user's schedule, and the zoned cron or fixed rate of a recognition action.
type scheduleParser struct{ cron.Parser }

// ScheduleParser is the parser of the action schedule worker.
func ScheduleParser() scheduleParser {
	return scheduleParser{cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)}
}

// ExecutionSubjectIDColumn and ExecutionSubjectTypeColumn scan f_execution_subject and
// f_execution_subject_type, in that order, into the action type's execution subject.
func (at *ActionType) ExecutionSubjectIDColumn() sqlScanner {
	return func(id string) {
		if id != "" {
			at.ExecutionSubject = &AccountInfo{ID: id}
		}
	}
}

func (at *ActionType) ExecutionSubjectTypeColumn() sqlScanner {
	return func(kind string) {
		if at.ExecutionSubject != nil {
			at.ExecutionSubject.Type = kind
		}
	}
}

// EventColumnValues are the values of f_execution_subject, f_execution_subject_type and
// f_rule_version.
func (at *ActionType) EventColumnValues() []any {
	id, kind := "", ""
	if at.ExecutionSubject != nil {
		id, kind = at.ExecutionSubject.ID, at.ExecutionSubject.Type
	}
	return []any{id, kind, at.RuleVersion}
}

// sqlScanner scans a string column into a setter.
type sqlScanner func(string)

func (s sqlScanner) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		s("")
	case string:
		s(v)
	case []byte:
		s(string(v))
	default:
		return fmt.Errorf("cannot scan %T into a string", src)
	}
	return nil
}
