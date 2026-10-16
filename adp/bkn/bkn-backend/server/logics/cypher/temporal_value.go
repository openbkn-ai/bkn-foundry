// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Temporal values retain their logical type internally. JSON uses ISO text;
// graph_v1's value_types identifies the type without changing scalar JSON.
type temporalValue struct {
	kind                               string
	instant                            time.Time
	zone                               string
	months, days, seconds, nanoseconds int64
}

func (value temporalValue) MarshalJSON() ([]byte, error) { return json.Marshal(value.String()) }
func (value temporalValue) String() string {
	if value.kind == "duration" {
		return value.durationString()
	}
	date := fmt.Sprintf("%04d-%02d-%02d", value.instant.Year(), value.instant.Month(), value.instant.Day())
	clock := fmt.Sprintf("%02d:%02d", value.instant.Hour(), value.instant.Minute())
	if value.instant.Second() != 0 || value.instant.Nanosecond() != 0 {
		clock += fmt.Sprintf(":%02d", value.instant.Second())
	}
	if value.instant.Nanosecond() != 0 {
		clock += "." + strings.TrimRight(fmt.Sprintf("%09d", value.instant.Nanosecond()), "0")
	}
	if value.kind == "date" {
		return date
	}
	result := clock
	if value.kind == "datetime" || value.kind == "localdatetime" {
		result = date + "T" + clock
	}
	if value.kind == "datetime" || value.kind == "time" {
		_, offset := value.instant.Zone()
		if offset == 0 {
			result += "Z"
		} else {
			sign := "+"
			if offset < 0 {
				sign = "-"
				offset = -offset
			}
			result += fmt.Sprintf("%s%02d:%02d", sign, offset/3600, (offset%3600)/60)
			if offset%60 != 0 {
				result += fmt.Sprintf(":%02d", offset%60)
			}
		}
		if value.kind == "datetime" && value.zone != "" && value.zone != "UTC" && value.zone != "Z" && !strings.HasPrefix(value.zone, "+") && !strings.HasPrefix(value.zone, "-") {
			result += "[" + value.zone + "]"
		}
	}
	return result
}
func (value temporalValue) field(name string) (any, error) {
	t := value.instant
	isoYear, week := t.ISOWeek()
	dayOfWeek := int(t.Weekday())
	if dayOfWeek == 0 {
		dayOfWeek = 7
	}
	quarter := (int(t.Month())-1)/3 + 1
	fields := map[string]any{"year": int64(t.Year()), "month": int64(t.Month()), "day": int64(t.Day()), "weekYear": int64(isoYear), "week": int64(week), "dayOfWeek": int64(dayOfWeek), "ordinalDay": int64(t.YearDay()), "quarter": int64(quarter), "dayOfQuarter": int64(t.YearDay() - time.Date(t.Year(), time.Month((quarter-1)*3+1), 1, 0, 0, 0, 0, t.Location()).YearDay() + 1), "hour": int64(t.Hour()), "minute": int64(t.Minute()), "second": int64(t.Second()), "millisecond": int64(t.Nanosecond() / 1000000), "microsecond": int64(t.Nanosecond() / 1000), "nanosecond": int64(t.Nanosecond()), "epochSeconds": t.Unix(), "epochMillis": t.UnixMilli()}
	if value.kind == "datetime" || value.kind == "time" {
		_, offset := t.Zone()
		fields["timezone"] = value.zone
		fields["offset"] = t.Format("-07:00")
		fields["offsetSeconds"] = int64(offset)
		fields["offsetMinutes"] = int64(offset / 60)
	}
	if value.kind == "duration" {
		return value.durationField(name)
	}
	if result, ok := fields[name]; ok {
		return result, nil
	}
	return nil, nil
}

func isTemporalFunction(name string) bool {
	root := strings.Split(name, ".")[0]
	return root == "date" || root == "time" || root == "localtime" || root == "datetime" || root == "localdatetime" || root == "duration" || name == "timestamp"
}

func init() {
	for _, kind := range []string{"date", "time", "localtime", "datetime", "localdatetime"} {
		expressionFunctions[kind] = expressionFunction{0, 1, kind}
		expressionFunctions[kind+".truncate"] = expressionFunction{2, 3, kind}
		for _, clock := range []string{"transaction", "statement", "realtime"} {
			expressionFunctions[kind+"."+clock] = expressionFunction{0, 1, kind}
		}
	}
	expressionFunctions["timestamp"] = expressionFunction{0, 0, "integer"}
	expressionFunctions["datetime.fromepoch"] = expressionFunction{2, 2, "datetime"}
	expressionFunctions["datetime.fromepochmillis"] = expressionFunction{1, 1, "datetime"}
	expressionFunctions["duration"] = expressionFunction{1, 1, "duration"}
	for _, name := range []string{"between", "inmonths", "indays", "inseconds"} {
		expressionFunctions["duration."+name] = expressionFunction{2, 2, "duration"}
	}
}

func normalizeTemporalKind(value temporalValue) temporalValue {
	t := value.instant
	switch value.kind {
	case "date":
		value.instant = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		value.zone = ""
	case "localdatetime":
		value.instant = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
		value.zone = ""
	case "localtime":
		value.instant = time.Date(2000, 1, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
		value.zone = ""
	case "time":
		_, offset := t.Zone()
		value.instant = time.Date(2000, 1, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.FixedZone("", offset))
		value.zone = t.Format("-07:00")
	}
	return value
}
