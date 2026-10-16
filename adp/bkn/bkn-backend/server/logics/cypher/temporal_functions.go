// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"strings"
	"time"
)

func evaluateTemporalFunction(name string, arguments []any, now time.Time) (any, error) {
	for _, argument := range arguments {
		if argument == nil {
			return nil, nil
		}
	}
	if name == "timestamp" {
		return now.UnixMilli(), nil
	}
	parts := strings.Split(name, ".")
	kind := parts[0]
	if kind == "duration" {
		if len(parts) == 1 {
			return constructDuration(arguments[0])
		}
		start, aOK := arguments[0].(temporalValue)
		end, bOK := arguments[1].(temporalValue)
		if !aOK || !bOK {
			return nil, expressionTypeError()
		}
		return temporalDifference(parts[1], start, end)
	}
	if name == "datetime.fromepoch" || name == "datetime.fromepochmillis" {
		seconds, err := expressionInteger(arguments[0])
		if err != nil {
			return nil, err
		}
		nano := int64(0)
		if name == "datetime.fromepochmillis" {
			nano = (seconds % 1000) * 1000000
			seconds /= 1000
		} else {
			nano, err = expressionInteger(arguments[1])
			if err != nil {
				return nil, err
			}
		}
		return temporalValue{kind: "datetime", instant: time.Unix(seconds, nano).UTC(), zone: "UTC"}, nil
	}
	if len(parts) > 1 && parts[1] == "truncate" {
		unit, ok := arguments[0].(string)
		if !ok {
			return nil, expressionTypeError()
		}
		value, ok := arguments[1].(temporalValue)
		if !ok {
			return nil, expressionTypeError()
		}
		truncated, err := truncateTemporal(unit, value)
		if err != nil {
			return nil, err
		}
		truncated.kind = kind
		if len(arguments) == 2 {
			return normalizeTemporalKind(truncated), nil
		}
		fields, ok := arguments[2].(map[string]any)
		if !ok {
			return nil, expressionTypeError()
		}
		// Truncation overrides wall-clock components rather than converting instants.
		truncated.kind = "localdatetime"
		overrides := map[string]any{"datetime": truncated}
		if strings.EqualFold(unit, "week") {
			year, week := truncated.instant.ISOWeek()
			overrides["year"] = int64(year)
			overrides["week"] = int64(week)
		}
		if strings.EqualFold(unit, "millisecond") {
			overrides["millisecond"] = int64(truncated.instant.Nanosecond() / 1000000)
		}
		if strings.EqualFold(unit, "microsecond") {
			overrides["microsecond"] = int64(truncated.instant.Nanosecond() / 1000)
		}
		for name, value := range fields {
			overrides[name] = value
		}
		return constructTemporal(kind, overrides, now)
	}
	var input any
	if len(arguments) > 0 {
		input = arguments[0]
	}
	if len(parts) > 1 && parts[1] == "realtime" {
		now = time.Now().UTC()
	}
	if fields, ok := input.(map[string]any); ok && len(fields) == 1 {
		if zone, ok := fields["timezone"].(string); ok {
			location, err := temporalLocation(zone)
			if err != nil {
				return nil, err
			}
			return temporalValue{kind: kind, instant: now.In(location), zone: zone}, nil
		}
	}
	return constructTemporal(kind, input, now)
}
func truncateTemporal(unit string, value temporalValue) (temporalValue, error) {
	t := value.instant
	y, m, d := t.Date()
	h, minute, second := t.Clock()
	nano := t.Nanosecond()
	resetClock := func() { h, minute, second, nano = 0, 0, 0, 0 }
	switch strings.ToLower(unit) {
	case "millennium":
		y = (y / 1000) * 1000
		m, d = 1, 1
		resetClock()
	case "century":
		y = (y / 100) * 100
		m, d = 1, 1
		resetClock()
	case "decade":
		y = (y / 10) * 10
		m, d = 1, 1
		resetClock()
	case "year":
		m, d = 1, 1
		resetClock()
	case "weekyear":
		isoYear, _ := t.ISOWeek()
		jan4 := time.Date(isoYear, 1, 4, 0, 0, 0, 0, t.Location())
		weekday := int(jan4.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		value.instant = jan4.AddDate(0, 0, -weekday+1)
		return value, nil
	case "quarter":
		m = time.Month((int(m)-1)/3*3 + 1)
		d = 1
		resetClock()
	case "month":
		d = 1
		resetClock()
	case "week":
		weekday := int(t.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		t = t.AddDate(0, 0, -weekday+1)
		y, m, d = t.Date()
		resetClock()
	case "day":
		resetClock()
	case "hour":
		minute, second, nano = 0, 0, 0
	case "minute":
		second, nano = 0, 0
	case "second":
		nano = 0
	case "millisecond":
		nano = (nano / 1000000) * 1000000
	case "microsecond":
		nano = (nano / 1000) * 1000
	default:
		return value, temporalError("unknown temporal truncation unit")
	}
	value.instant = time.Date(y, m, d, h, minute, second, nano, t.Location())
	return value, nil
}

func evaluateTemporalArithmetic(operator string, values []any) (any, bool, error) {
	if operator != "+" && operator != "-" && operator != "*" && operator != "/" && operator != "NEGATE" && operator != "POSITIVE" {
		return nil, false, nil
	}
	if len(values) == 0 {
		return nil, false, nil
	}
	first, left := values[0].(temporalValue)
	if len(values) == 1 {
		if left && first.kind == "duration" && (operator == "NEGATE" || operator == "POSITIVE") {
			sign := int64(1)
			if operator == "NEGATE" {
				sign = -1
			}
			value, err := addTemporalDuration(temporalValue{kind: "duration"}, first, sign)
			return value, true, err
		}
		return nil, false, nil
	}
	second, right := values[1].(temporalValue)
	if !left && !right {
		return nil, false, nil
	}
	if left && right && (operator == "+" || operator == "-") {
		sign := int64(1)
		if operator == "-" {
			sign = -1
		}
		if first.kind == "duration" && second.kind != "duration" && operator == "+" {
			first, second = second, first
		}
		value, err := addTemporalDuration(first, second, sign)
		return value, true, err
	}
	if right && second.kind == "duration" && operator == "*" {
		first, values[1], left = second, values[0], true
	}
	if left && first.kind == "duration" && (operator == "*" || operator == "/") {
		number, ok := expressionFloat(values[1])
		if !ok {
			return nil, true, expressionTypeError()
		}
		if operator == "/" {
			if number == 0 {
				return nil, true, expressionArithmeticError("division by zero")
			}
			number = 1 / number
		}
		value, err := constructDuration(map[string]any{"months": float64(first.months) * number, "days": float64(first.days) * number, "seconds": float64(first.seconds) * number, "nanoseconds": float64(first.nanoseconds) * number})
		return value, true, err
	}
	return nil, true, expressionTypeError()
}
