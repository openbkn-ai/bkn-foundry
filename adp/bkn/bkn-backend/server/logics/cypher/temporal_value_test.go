// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestTemporalArithmeticRangeAndPrecision(t *testing.T) {
	for _, input := range []struct {
		left, right temporalValue
		sign        int64
	}{
		{temporalValue{kind: "duration", seconds: math.MaxInt64}, temporalValue{kind: "duration", seconds: 1}, 1},
		{temporalValue{kind: "duration", months: math.MinInt64}, temporalValue{kind: "duration", months: 1}, -1},
		{temporalValue{kind: "datetime", instant: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}, temporalValue{kind: "duration", seconds: math.MaxInt64}, 1},
	} {
		if _, err := addTemporalDuration(input.left, input.right, input.sign); err == nil {
			t.Fatal("temporal overflow accepted")
		}
	}
	start := temporalValue{kind: "datetime", instant: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
	result, err := addTemporalDuration(start, temporalValue{kind: "duration", seconds: 12622780800}, 1)
	if err != nil || result.instant.Year() != 2400 {
		t.Fatalf("400-year clock addition wrapped: %s %v", result.String(), err)
	}
	if _, err := (temporalValue{kind: "duration", seconds: math.MaxInt64}).durationField("nanoseconds"); err == nil {
		t.Fatal("duration field overflow accepted")
	}
	order, err := expressionCompare(temporalValue{kind: "duration", seconds: math.MaxInt64, nanoseconds: 1}, temporalValue{kind: "duration", seconds: math.MaxInt64, nanoseconds: 2})
	if err != nil || order >= 0 {
		t.Fatal("duration comparison lost nanoseconds", order, err)
	}
}

func TestTemporalResultsAndQueryClock(t *testing.T) {
	service, _, _ := graphService(t)
	request := graphRequest("RETURN date('2024-01-31')+duration('P1M') AS day,datetime()=datetime.statement() AS same,timestamp()=datetime().epochMillis AS clock")
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Entries[0]["day"].(temporalValue).String() != "2024-02-29" || result.Entries[0]["same"] != true || result.Entries[0]["clock"] != true {
		t.Fatal("invalid temporal result", result.Entries)
	}
	if result.ValueTypes["day"].Kind != "date" {
		t.Fatal("temporal result type missing")
	}
}
