// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"strconv"
	"strings"
	"time"
)

// A record-event action type takes the time of its check where a value is relative to it: in its
// condition and in the parameters of its targets (SPECIFICATION.md §8.5, §8.9).
const (
	// ValueFromCheckTime is the check time plus an optional ISO 8601 duration, such as -PT48H.
	ValueFromCheckTime = "check_time"
	// ValueFromCheckDate is the day of the check in the schedule's time zone plus an optional ISO
	// 8601 duration of whole days, weeks, months or years, such as -P3D.
	ValueFromCheckDate = "check_date"
)

// CheckOffset is an ISO 8601 duration added to the time or the day of a check.
type CheckOffset struct {
	Years, Months, Days int
	Clock               time.Duration
}

// ParseCheckOffset reads an ISO 8601 duration such as -PT48H, P1DT12H or -P3D. An empty value is
// no offset. Only a check time takes the hours, minutes and seconds after T.
func ParseCheckOffset(value string, date bool) (CheckOffset, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return CheckOffset{}, nil
	}
	sign := 1
	switch text[0] {
	case '-':
		sign, text = -1, text[1:]
	case '+':
		text = text[1:]
	}
	if !strings.HasPrefix(text, "P") || len(text) == 1 {
		return CheckOffset{}, specErrorf("%q is not an ISO 8601 duration such as -PT48H or -P3D", value)
	}
	text = text[1:]
	var offset CheckOffset
	clock := false
	seen := ""
	for text != "" {
		if text[0] == 'T' {
			if clock || len(text) == 1 {
				return CheckOffset{}, specErrorf("%q is not an ISO 8601 duration such as -PT48H or -P3D", value)
			}
			if date {
				return CheckOffset{}, specErrorf("%q: a check date moves by whole days, such as -P3D", value)
			}
			clock, text = true, text[1:]
			continue
		}
		end := 0
		for end < len(text) && text[end] >= '0' && text[end] <= '9' {
			end++
		}
		if end == 0 || end == len(text) {
			return CheckOffset{}, specErrorf("%q is not an ISO 8601 duration such as -PT48H or -P3D", value)
		}
		n, err := strconv.Atoi(text[:end])
		if err != nil || n > 1_000_000 {
			return CheckOffset{}, specErrorf("%q is not an ISO 8601 duration such as -PT48H or -P3D", value)
		}
		unit := text[end]
		key := string(unit)
		if clock {
			key = "T" + key
		}
		if strings.Contains(seen, key+",") {
			return CheckOffset{}, specErrorf("%q names %c twice", value, unit)
		}
		seen += key + ","
		switch {
		case !clock && unit == 'Y':
			offset.Years = sign * n
		case !clock && unit == 'M':
			offset.Months = sign * n
		case !clock && unit == 'W':
			offset.Days += sign * n * 7
		case !clock && unit == 'D':
			offset.Days += sign * n
		case clock && unit == 'H':
			offset.Clock += time.Duration(sign*n) * time.Hour
		case clock && unit == 'M':
			offset.Clock += time.Duration(sign*n) * time.Minute
		case clock && unit == 'S':
			offset.Clock += time.Duration(sign*n) * time.Second
		default:
			return CheckOffset{}, specErrorf("%q is not an ISO 8601 duration such as -PT48H or -P3D", value)
		}
		text = text[end+1:]
	}
	return offset, nil
}

// Apply moves t by the offset: the years, months and days on the calendar of t's location, then
// the clock.
func (o CheckOffset) Apply(t time.Time) time.Time {
	return t.AddDate(o.Years, o.Months, o.Days).Add(o.Clock)
}
