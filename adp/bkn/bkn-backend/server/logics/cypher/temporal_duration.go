// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func normalizeDuration(value temporalValue) temporalValue {
	value.seconds += value.nanoseconds / 1000000000
	value.nanoseconds %= 1000000000
	if value.nanoseconds < 0 {
		value.seconds--
		value.nanoseconds += 1000000000
	}
	return value
}
func (value temporalValue) durationString() string {
	value = normalizeDuration(value)
	if value.seconds < 0 && value.nanoseconds > 0 {
		value.seconds++
		value.nanoseconds -= 1000000000
	}
	result := "P"
	if years := value.months / 12; years != 0 {
		result += fmt.Sprintf("%dY", years)
	}
	if months := value.months % 12; months != 0 {
		result += fmt.Sprintf("%dM", months)
	}
	if value.days != 0 {
		result += fmt.Sprintf("%dD", value.days)
	}
	hours, minutes, seconds := value.seconds/3600, (value.seconds%3600)/60, value.seconds%60
	if hours != 0 || minutes != 0 || seconds != 0 || value.nanoseconds != 0 {
		result += "T"
		if hours != 0 {
			result += fmt.Sprintf("%dH", hours)
		}
		if minutes != 0 {
			result += fmt.Sprintf("%dM", minutes)
		}
		if seconds != 0 || value.nanoseconds != 0 {
			sign := ""
			if seconds < 0 || value.nanoseconds < 0 {
				sign = "-"
			}
			secondsText := fmt.Sprintf("%s%d", sign, absInt64(seconds))
			if value.nanoseconds != 0 {
				secondsText += "." + strings.TrimRight(fmt.Sprintf("%09d", absInt64(value.nanoseconds)), "0")
			}
			result += secondsText + "S"
		}
	}
	if result == "P" {
		return "PT0S"
	}
	return result
}
func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
func (value temporalValue) durationField(name string) (any, error) {
	units := map[string]int64{"milliseconds": 1000, "microseconds": 1000000, "nanoseconds": 1000000000}
	if scale, ok := units[name]; ok {
		result := new(big.Int).Mul(big.NewInt(value.seconds), big.NewInt(scale))
		result.Add(result, big.NewInt(value.nanoseconds/(1000000000/scale)))
		if !result.IsInt64() {
			return nil, temporalError("duration field is out of range")
		}
		return result.Int64(), nil
	}
	fields := map[string]int64{"years": value.months / 12, "quarters": value.months / 3, "months": value.months, "weeks": value.days / 7, "days": value.days, "hours": value.seconds / 3600, "minutes": value.seconds / 60, "seconds": value.seconds, "milliseconds": value.seconds*1000 + value.nanoseconds/1000000, "microseconds": value.seconds*1000000 + value.nanoseconds/1000, "nanoseconds": value.seconds*1000000000 + value.nanoseconds, "monthsOfYear": value.months % 12, "monthsOfQuarter": value.months % 3, "quartersOfYear": (value.months / 3) % 4, "daysOfWeek": value.days % 7, "hoursOfDay": (value.seconds / 3600) % 24, "minutesOfHour": (value.seconds / 60) % 60, "secondsOfMinute": value.seconds % 60, "millisecondsOfSecond": value.nanoseconds / 1000000, "microsecondsOfSecond": value.nanoseconds / 1000, "nanosecondsOfSecond": value.nanoseconds}
	if result, ok := fields[name]; ok {
		return result, nil
	}
	return nil, nil
}

var isoDateDuration = regexp.MustCompile(`^P([0-9]+)-([0-9]{2})-([0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2}(?:\.[0-9]+)?)$`)
var isoDuration = regexp.MustCompile(`^([+-]?)P(?:([-+]?[0-9]+(?:[.,][0-9]+)?)Y)?(?:([-+]?[0-9]+(?:[.,][0-9]+)?)M)?(?:([-+]?[0-9]+(?:[.,][0-9]+)?)W)?(?:([-+]?[0-9]+(?:[.,][0-9]+)?)D)?(?:T(?:([-+]?[0-9]+(?:[.,][0-9]+)?)H)?(?:([-+]?[0-9]+(?:[.,][0-9]+)?)M)?(?:([-+]?[0-9]+(?:[.,][0-9]+)?)S)?)?$`)

func constructDuration(input any) (temporalValue, error) {
	if existing, ok := input.(temporalValue); ok && existing.kind == "duration" {
		return existing, nil
	}
	fields, ok := input.(map[string]any)
	if text, isString := input.(string); isString {
		if match := isoDateDuration.FindStringSubmatch(text); match != nil {
			fields = map[string]any{}
			for i, name := range []string{"years", "months", "days", "hours", "minutes", "seconds"} {
				if match[i+1] != "" {
					number, err := strconv.ParseFloat(match[i+1], 64)
					if err != nil {
						return temporalValue{}, err
					}
					fields[name] = number
				}
			}
			return constructDuration(fields)
		}
		match := isoDuration.FindStringSubmatch(text)
		if match == nil || text == "P" || text == "PT" {
			return temporalValue{}, temporalError("invalid duration text")
		}
		fields = map[string]any{}
		sign := float64(1)
		if match[1] == "-" {
			sign = -1
		}
		for i, name := range []string{"years", "months", "weeks", "days", "hours", "minutes", "seconds"} {
			if match[i+2] != "" {
				number, err := strconv.ParseFloat(strings.ReplaceAll(match[i+2], ",", "."), 64)
				if err != nil {
					return temporalValue{}, err
				}
				fields[name] = sign * number
			}
		}
		ok = true
	}
	if !ok {
		return temporalValue{}, expressionTypeError()
	}
	parts := map[string]float64{}
	for name, raw := range fields {
		number, ok := expressionFloat(raw)
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
			return temporalValue{}, expressionTypeError()
		}
		parts[name] = number
	}
	months := parts["years"]*12 + parts["quarters"]*3 + parts["months"]
	monthSeconds := (months - math.Trunc(months)) * 2629746
	carryDays := math.Trunc(monthSeconds / 86400)
	days := parts["weeks"]*7 + parts["days"] + carryDays
	seconds := parts["hours"]*3600 + parts["minutes"]*60 + parts["seconds"] + (monthSeconds - carryDays*86400) + (days-math.Trunc(days))*86400
	nanos := (seconds-math.Trunc(seconds))*1e9 + parts["milliseconds"]*1e6 + parts["microseconds"]*1e3 + parts["nanoseconds"]
	if math.Abs(months) >= 9223372036854775808.0 || math.Abs(days) >= 9223372036854775808.0 || math.Abs(seconds) >= 9223372036854775808.0 || math.Abs(nanos) >= 9223372036854775808.0 {
		return temporalValue{}, temporalError("duration is out of range")
	}
	return checkedDuration(temporalValue{kind: "duration", months: int64(months), days: int64(days), seconds: int64(seconds), nanoseconds: int64(math.Round(nanos))})
}
func addTemporalMonths(t time.Time, months int64) time.Time {
	first := time.Date(t.Year(), t.Month(), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()).AddDate(0, int(months), 0)
	day := t.Day()
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, first.Location()).Day()
	if day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// Arithmetic is checked before normalization; neither int64 wraparound nor
// time.Duration's roughly 292-year range may silently change a graph value.
func checkedTemporalSum(left, right int64, sign int64) (int64, error) {
	value := new(big.Int).Mul(big.NewInt(right), big.NewInt(sign))
	value.Add(value, big.NewInt(left))
	if !value.IsInt64() {
		return 0, temporalError("temporal arithmetic is out of range")
	}
	return value.Int64(), nil
}
func checkedDuration(value temporalValue) (temporalValue, error) {
	seconds, err := checkedTemporalSum(value.seconds, value.nanoseconds/1000000000, 1)
	if err != nil {
		return temporalValue{}, err
	}
	value.seconds, value.nanoseconds = seconds, value.nanoseconds%1000000000
	if value.nanoseconds < 0 {
		value.seconds, err = checkedTemporalSum(value.seconds, 1, -1)
		value.nanoseconds += 1000000000
	}
	return value, err
}
func addTemporalDuration(value, amount temporalValue, sign int64) (temporalValue, error) {
	if amount.kind != "duration" {
		return value, expressionTypeError()
	}
	if value.kind == "duration" {
		var err error
		value.months, err = checkedTemporalSum(value.months, amount.months, sign)
		if err != nil {
			return temporalValue{}, err
		}
		value.days, err = checkedTemporalSum(value.days, amount.days, sign)
		if err != nil {
			return temporalValue{}, err
		}
		value.seconds, err = checkedTemporalSum(value.seconds, amount.seconds, sign)
		if err != nil {
			return temporalValue{}, err
		}
		value.nanoseconds, err = checkedTemporalSum(value.nanoseconds, amount.nanoseconds, sign)
		if err != nil {
			return temporalValue{}, err
		}
		return checkedDuration(value)
	}
	if amount.months < -24000000000 || amount.months > 24000000000 || amount.days < -731000000000 || amount.days > 731000000000 {
		return temporalValue{}, temporalError("calendar arithmetic is out of range")
	}
	value.instant = addTemporalMonths(value.instant, sign*amount.months).AddDate(0, 0, int(sign*amount.days))
	if value.instant.Year() < -999999999 || value.instant.Year() > 999999999 {
		return temporalValue{}, temporalError("calendar arithmetic is out of range")
	}
	if value.kind != "date" {
		seconds, err := checkedTemporalSum(value.instant.Unix(), amount.seconds, sign)
		if err != nil || seconds < -31557000000000000 || seconds > 31557000000000000 {
			return temporalValue{}, temporalError("clock arithmetic is out of range")
		}
		value.instant = time.Unix(seconds, int64(value.instant.Nanosecond())+sign*amount.nanoseconds).In(value.instant.Location())
	}
	if value.instant.Year() < -999999999 || value.instant.Year() > 999999999 {
		return temporalValue{}, temporalError("calendar arithmetic is out of range")
	}
	return normalizeTemporalKind(value), nil
}
func temporalDifference(kind string, start, end temporalValue) (temporalValue, error) {
	if start.kind == "duration" || end.kind == "duration" {
		return temporalValue{}, expressionTypeError()
	}
	first, last := start.instant, end.instant
	startDate := start.kind != "time" && start.kind != "localtime"
	endDate := end.kind != "time" && end.kind != "localtime"
	startAware := start.kind == "time" || start.kind == "datetime"
	endAware := end.kind == "time" || end.kind == "datetime"
	wall := func(t time.Time, ignoreDate, ignoreZone bool) time.Time {
		year, month, day := t.Date()
		if ignoreDate {
			year, month, day = 2000, 1, 1
		}
		location := t.Location()
		if ignoreZone {
			location = time.UTC
		} else if ignoreDate {
			_, offset := t.Zone()
			location = time.FixedZone("", offset)
		}
		return time.Date(year, month, day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), location)
	}
	if kind == "inseconds" && startAware != endAware && (start.kind == "datetime" || end.kind == "datetime") {
		// The missing offset is obtained from the zoned endpoint's region at
		// the other endpoint's local date, including a daylight-saving transition.
		if startAware {
			y, m, d := last.Date()
			if !endDate {
				y, m, d = first.Date()
			}
			last = time.Date(y, m, d, last.Hour(), last.Minute(), last.Second(), last.Nanosecond(), first.Location())
		} else {
			y, m, d := first.Date()
			if !startDate {
				y, m, d = last.Date()
			}
			first = time.Date(y, m, d, first.Hour(), first.Minute(), first.Second(), first.Nanosecond(), last.Location())
		}
		startAware, endAware = true, true
		if startDate != endDate {
			startDate, endDate = true, true
		}
	}
	first = wall(first, !startDate || !endDate, !startAware || !endAware)
	last = wall(last, !startDate || !endDate, !startAware || !endAware)

	result := temporalValue{kind: "duration"}
	dateCapable := start.kind != "time" && start.kind != "localtime" && end.kind != "time" && end.kind != "localtime"
	if kind == "between" || kind == "inmonths" {
		if dateCapable {
			months := int64((last.Year()-first.Year())*12 + int(last.Month()-first.Month()))
			anchor := addTemporalMonths(first, months)
			if months > 0 && anchor.After(last) {
				months--
				anchor = addTemporalMonths(first, months)
			} else if months < 0 && anchor.Before(last) {
				months++
				anchor = addTemporalMonths(first, months)
			}
			result.months = months
			first = anchor
		}
		if kind == "inmonths" {
			return result, nil
		}
	}
	if kind == "between" || kind == "indays" {
		if dateCapable {
			a := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.UTC)
			b := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, time.UTC)
			days := (b.Unix() - a.Unix()) / 86400
			anchor := first.AddDate(0, 0, int(days))
			if days > 0 && anchor.After(last) {
				days--
				anchor = first.AddDate(0, 0, int(days))
			} else if days < 0 && anchor.Before(last) {
				days++
				anchor = first.AddDate(0, 0, int(days))
			}
			result.days = days
			first = anchor
		}
		if kind == "indays" {
			return result, nil
		}
	}
	result.seconds = last.Unix() - first.Unix()
	result.nanoseconds = int64(last.Nanosecond() - first.Nanosecond())
	return normalizeDuration(result), nil
}
