// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func temporalError(message string) error {
	return &expressionError{Kind: "temporal_error", Message: message}
}
func temporalInt(fields map[string]any, name string, fallback int) (int, error) {
	value, ok := fields[name]
	if !ok {
		return fallback, nil
	}
	number, err := expressionInteger(value)
	if err != nil || number < -1000000000 || number > 1000000000 {
		return 0, temporalError("invalid temporal component " + name)
	}
	return int(number), nil
}
func temporalLocation(zone string) (*time.Location, error) {
	if zone == "" || zone == "Z" || zone == "UTC" {
		return time.UTC, nil
	}
	if strings.HasPrefix(zone, "+") || strings.HasPrefix(zone, "-") {
		raw := strings.ReplaceAll(zone[1:], ":", "")
		if len(raw) != 2 && len(raw) != 4 && len(raw) != 6 {
			return nil, temporalError("invalid time-zone offset")
		}
		hour, err := strconv.Atoi(raw[:2])
		if err != nil {
			return nil, err
		}
		minute, second := 0, 0
		if len(raw) >= 4 {
			minute, err = strconv.Atoi(raw[2:4])
			if err != nil {
				return nil, err
			}
		}
		if len(raw) == 6 {
			second, err = strconv.Atoi(raw[4:])
			if err != nil {
				return nil, err
			}
		}
		if hour > 18 || minute > 59 || second > 59 {
			return nil, temporalError("invalid time-zone offset")
		}
		offset := hour*3600 + minute*60 + second
		if zone[0] == '-' {
			offset = -offset
		}
		return time.FixedZone(zone, offset), nil
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, temporalError("unknown time zone")
	}
	return location, nil
}
func constructTemporal(kind string, input any, now time.Time) (temporalValue, error) {
	result := temporalValue{kind: kind}
	if input == nil {
		result.instant = now
		result.zone = "UTC"
		return normalizeTemporalKind(result), nil
	}
	if other, ok := input.(temporalValue); ok {
		result.instant = other.instant
		result.zone = other.zone
		return normalizeTemporalKind(result), nil
	}
	if text, ok := input.(string); ok {
		return parseTemporal(kind, text)
	}
	fields, ok := input.(map[string]any)
	if !ok {
		return result, expressionTypeError()
	}
	base := time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
	hasBase, baseAware := false, false
	var convertLocation *time.Location
	for _, name := range []string{"datetime", "date", "time"} {
		if value, exists := fields[name]; exists {
			other, ok := value.(temporalValue)
			if !ok {
				return result, expressionTypeError()
			}
			next := other.instant
			if name == "time" && kind == "time" && other.kind == "datetime" {
				_, offset := next.Zone()
				next = next.In(time.FixedZone("", offset))
			}
			switch name {
			case "datetime":
				base = next
			case "date":
				base = time.Date(next.Year(), next.Month(), next.Day(), base.Hour(), base.Minute(), base.Second(), base.Nanosecond(), base.Location())
			case "time":
				base = time.Date(base.Year(), base.Month(), base.Day(), next.Hour(), next.Minute(), next.Second(), next.Nanosecond(), next.Location())
			}
			if name != "date" {
				result.zone = other.zone
				baseAware = other.kind == "datetime" || other.kind == "time"
			}
			hasBase = true
		}
	}

	if value, exists := fields["timezone"]; exists {
		zone, ok := value.(string)
		if !ok {
			return result, expressionTypeError()
		}
		location, err := temporalLocation(zone)
		if err != nil {
			return result, err
		}
		result.zone = zone
		if hasBase && baseAware && (kind == "datetime" || kind == "time") {
			convertLocation = location
		} else {
			base = time.Date(base.Year(), base.Month(), base.Day(), base.Hour(), base.Minute(), base.Second(), base.Nanosecond(), location)
		}
	}
	year, err := temporalInt(fields, "year", base.Year())
	if err != nil {
		return result, err
	}
	month, err := temporalInt(fields, "month", int(base.Month()))
	if err != nil {
		return result, err
	}
	day, err := temporalInt(fields, "day", base.Day())
	if err != nil {
		return result, err
	}
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, base.Location())
	if month < 1 || month > 12 || day < 1 || day > 31 || date.Month() != time.Month(month) || date.Day() != day {
		return result, temporalError("invalid calendar date")
	}
	if _, ok := fields["week"]; ok {
		isoYear, baseWeek := base.ISOWeek()
		if _, exists := fields["year"]; !exists && hasBase {
			year = isoYear
		}
		week, err := temporalInt(fields, "week", baseWeek)
		if err != nil {
			return result, err
		}
		weekday := int(base.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		if !hasBase {
			weekday = 1
		}
		weekday, err = temporalInt(fields, "dayOfWeek", weekday)
		if err != nil {
			return result, err
		}
		if week < 1 || week > 53 || weekday < 1 || weekday > 7 {
			return result, temporalError("invalid week date")
		}
		jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, base.Location())
		offset := int(jan4.Weekday())
		if offset == 0 {
			offset = 7
		}
		date = jan4.AddDate(0, 0, -offset+1+(week-1)*7+weekday-1)
	} else if _, ok := fields["ordinalDay"]; ok {
		ordinal, err := temporalInt(fields, "ordinalDay", 1)
		if err != nil {
			return result, err
		}
		date = time.Date(year, 1, ordinal, 0, 0, 0, 0, base.Location())
		if ordinal < 1 || date.Year() != year {
			return result, temporalError("invalid ordinal date")
		}
	} else if _, ok := fields["quarter"]; ok {
		quarter, err := temporalInt(fields, "quarter", 1)
		if err != nil {
			return result, err
		}
		defaultOrdinal := 1
		if hasBase {
			defaultOrdinal = base.YearDay() - time.Date(base.Year(), time.Month((int(base.Month())-1)/3*3+1), 1, 0, 0, 0, 0, base.Location()).YearDay() + 1
		}
		ordinal, err := temporalInt(fields, "dayOfQuarter", defaultOrdinal)
		if err != nil {
			return result, err
		}
		date = time.Date(year, time.Month((quarter-1)*3+1), ordinal, 0, 0, 0, 0, base.Location())
		if quarter < 1 || quarter > 4 || ordinal < 1 || (int(date.Month())-1)/3+1 != quarter {
			return result, temporalError("invalid quarter date")
		}
	}
	hour, err := temporalInt(fields, "hour", base.Hour())
	if err != nil {
		return result, err
	}
	minute, err := temporalInt(fields, "minute", base.Minute())
	if err != nil {
		return result, err
	}
	second, err := temporalInt(fields, "second", base.Second())
	if err != nil {
		return result, err
	}
	nano := base.Nanosecond()
	for _, name := range []string{"millisecond", "microsecond", "nanosecond"} {
		if _, exists := fields[name]; exists {
			nano = 0
			break
		}
	}
	for _, part := range []struct {
		name       string
		multiplier int
	}{{"millisecond", 1000000}, {"microsecond", 1000}, {"nanosecond", 1}} {
		if _, exists := fields[part.name]; exists {
			value, err := temporalInt(fields, part.name, 0)
			if err != nil {
				return result, err
			}
			nano += value * part.multiplier
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 || nano < 0 || nano >= 1000000000 {
		return result, temporalError("invalid clock time")
	}
	result.instant = time.Date(date.Year(), date.Month(), date.Day(), hour, minute, second, nano, date.Location())
	if convertLocation != nil {
		result.instant = result.instant.In(convertLocation)
	}
	if kind == "localtime" || kind == "localdatetime" || kind == "date" {
		t := result.instant
		result.instant = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	}
	if result.zone == "" && (kind == "datetime" || kind == "time") {
		result.zone = "UTC"
	}
	return normalizeTemporalKind(result), nil
}

var isoDate = regexp.MustCompile(`^([+-]?[0-9]{4,})(?:-?([0-9]{2})(?:-?([0-9]{2}))?)?$`)
var isoWeek = regexp.MustCompile(`^([+-]?[0-9]{4,})-?W([0-9]{2})(?:-?([1-7]))?$`)
var isoOrdinal = regexp.MustCompile(`^([+-]?[0-9]{4,})-([0-9]{3})$`)
var isoClock = regexp.MustCompile(`^([0-9]{2})(?::?([0-9]{2}))?(?::?([0-9]{2}))?(?:[.,]([0-9]{1,9}))?$`)

func parseTemporal(kind, text string) (temporalValue, error) {
	fields := map[string]any{}
	text = strings.TrimSpace(text)
	if index := strings.Index(text, "["); index >= 0 {
		if !strings.HasSuffix(text, "]") {
			return temporalValue{}, temporalError("invalid named time zone")
		}
		fields["timezone"] = text[index+1 : len(text)-1]
		text = text[:index]
	}
	datePart, clockPart := "", ""
	switch kind {
	case "date":
		datePart = text
	case "localdatetime", "datetime":
		parts := strings.SplitN(text, "T", 2)
		datePart = parts[0]
		if len(parts) > 1 {
			clockPart = parts[1]
		}
	default:
		clockPart = strings.TrimPrefix(text, "T")
	}
	number := func(raw string) int64 { value, _ := strconv.ParseInt(raw, 10, 64); return value }
	if datePart != "" {
		if len(datePart) == 8 && !strings.ContainsAny(datePart, "-+W") {
			datePart = datePart[:4] + "-" + datePart[4:6] + "-" + datePart[6:]
		} else if len(datePart) == 6 && !strings.ContainsAny(datePart, "-+W") {
			datePart = datePart[:4] + "-" + datePart[4:]
		} else if len(datePart) == 7 && !strings.ContainsAny(datePart, "-+W") {
			datePart = datePart[:4] + "-" + datePart[4:]
		}
		if match := isoWeek.FindStringSubmatch(datePart); match != nil {
			fields["year"] = number(match[1])
			fields["week"] = number(match[2])
			if match[3] != "" {
				fields["dayOfWeek"] = number(match[3])
			}
		} else if match := isoOrdinal.FindStringSubmatch(datePart); match != nil {
			fields["year"] = number(match[1])
			fields["ordinalDay"] = number(match[2])
		} else if match := isoDate.FindStringSubmatch(datePart); match != nil {
			fields["year"] = number(match[1])
			if match[2] != "" {
				fields["month"] = number(match[2])
			}
			if match[3] != "" {
				fields["day"] = number(match[3])
			}
		} else {
			return temporalValue{}, temporalError("invalid date text")
		}
	}
	if clockPart != "" {
		if strings.HasSuffix(clockPart, "Z") {
			fields["timezone"] = "UTC"
			clockPart = strings.TrimSuffix(clockPart, "Z")
		} else if index := strings.IndexAny(clockPart, "+-"); index >= 0 {
			if _, hasNamed := fields["timezone"]; !hasNamed {
				fields["timezone"] = clockPart[index:]
			}
			clockPart = clockPart[:index]
		}
		match := isoClock.FindStringSubmatch(clockPart)
		if match == nil {
			return temporalValue{}, temporalError("invalid clock text")
		}
		for i, name := range []string{"hour", "minute", "second"} {
			if match[i+1] != "" {
				fields[name] = number(match[i+1])
			}
		}
		if match[4] != "" {
			fields["nanosecond"] = number(match[4] + strings.Repeat("0", 9-len(match[4])))
		}
	}
	value, err := constructTemporal(kind, fields, time.Time{})
	if err != nil {
		return value, fmt.Errorf("%w", err)
	}
	return value, nil
}
