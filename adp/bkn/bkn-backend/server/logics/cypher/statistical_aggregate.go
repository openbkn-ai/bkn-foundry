// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"math"
	"sort"
)

var valueAggregateFunctions = map[string]bool{"collect": true, "stdev": true, "stdevp": true, "percentilecont": true, "percentiledisc": true}

func aggregatePercentile(function string, pairs []any) (any, error) {
	values := []float64{}
	var percentile float64
	hasPercentile := false
	for _, row := range pairs {
		pair, ok := row.([]any)
		if !ok || len(pair) != 2 {
			return nil, expressionTypeError()
		}
		p, ok := expressionFloat(pair[1])
		if !ok {
			return nil, expressionTypeError()
		}
		if math.IsNaN(p) || p < 0 || p > 1 {
			return nil, &expressionError{Kind: "argument_out_of_range", Message: "percentile must be between zero and one"}
		}
		if hasPercentile && percentile != p {
			return nil, &expressionError{Kind: "invalid_argument", Message: "percentile must be constant within a group"}
		}
		percentile, hasPercentile = p, true
		if pair[0] == nil {
			continue
		}
		number, ok := expressionFloat(pair[0])
		if !ok {
			return nil, expressionTypeError()
		}
		values = append(values, number)
	}
	if len(values) == 0 {
		return nil, nil
	}
	sort.Float64s(values)
	if function == "percentiledisc" {
		index := max(0, int(math.Ceil(percentile*float64(len(values))))-1)
		return values[index], nil
	}
	position := percentile * float64(len(values)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	fraction := position - float64(lower)
	return values[lower]*(1-fraction) + values[upper]*fraction, nil
}

func aggregateStandardDeviation(function string, values []any) (any, error) {
	if len(values) == 0 {
		return float64(0), nil
	}
	mean, m2 := float64(0), float64(0)
	for index, value := range values {
		number, ok := expressionFloat(value)
		if !ok {
			return nil, expressionTypeError()
		}
		delta := number - mean
		mean += delta / float64(index+1)
		m2 += delta * (number - mean)
	}
	if len(values) == 1 {
		return float64(0), nil
	}
	denominator := len(values)
	if function == "stdev" {
		denominator--
	}
	return math.Sqrt(m2 / float64(denominator)), nil
}
