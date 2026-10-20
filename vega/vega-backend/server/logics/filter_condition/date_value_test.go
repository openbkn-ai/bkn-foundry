// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package filter_condition

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTemporalValue(t *testing.T) {
	t.Run("equivalent clocks have one canonical representation", func(t *testing.T) {
		for _, value := range []any{"12:00:00.1", "12:00:00.100", "12:00:00.100000000", time.Date(2026, 10, 19, 12, 0, 0, 100000000, time.UTC)} {
			got, err := ParseTemporalValue(interfaces.DataType_Time, value, "UTC")
			require.NoError(t, err)
			assert.Equal(t, "12:00:00.1", got)
		}
	})
	t.Run("fractional precision", func(t *testing.T) {
		for _, kind := range []string{interfaces.DataType_Datetime, interfaces.DataType_Time} {
			prefixes := []string{"14:30:00"}
			suffixes := []string{""}
			if kind == interfaces.DataType_Datetime {
				prefixes = []string{"2026-10-19 14:30:00", "2026-10-19T14:30:00"}
				suffixes = []string{"", "+08:00"}
			}
			for i, prefix := range prefixes {
				for _, fraction := range []string{"", ".1", ".12", ".123", ".1234", ".0000", ".12345", ".123456", ".1234567", ".12345678", ".123456789"} {
					parsed, err := ParseTemporalValue(kind, prefix+fraction+suffixes[i], "Asia/Shanghai")
					require.NoError(t, err)
					if kind == interfaces.DataType_Time {
						clock, err := time.Parse(common.TimeLayout, prefix+fraction)
						require.NoError(t, err)
						assert.Equal(t, clock.Format(common.TimeLayout), parsed)
					} else {
						want, parseErr := time.Parse(common.TimeLayout, "14:30:00"+fraction)
						require.NoError(t, parseErr)
						assert.Equal(t, want.Nanosecond(), parsed.(time.Time).Nanosecond())
					}
				}
				for _, fraction := range []string{".1234567891", ".0000000000"} {
					_, err := ParseTemporalValue(kind, prefix+fraction+suffixes[i], "Asia/Shanghai")
					require.ErrorContains(t, err, "9 fractional digits")
				}
			}
		}
	})

	midnight := time.Date(2026, 10, 18, 16, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, fieldType, zone string
		value                 any
		want                  time.Time
	}{
		{"local datetime", "datetime", "Asia/Shanghai", "2026-10-19 00:00:00", midnight},
		{"UTC datetime", "datetime", "UTC", "2026-10-19 00:00:00", midnight.Add(8 * time.Hour)},
		{"fractional seconds", "datetime", "Asia/Shanghai", "2026-10-19 00:00:00.123", midnight.Add(123 * time.Millisecond)},
		{"explicit offset", "datetime", "UTC", "2026-10-19T00:00:00+08:00", midnight},
		{"UTC offset", "datetime", "Asia/Shanghai", "2026-10-18T16:00:00Z", midnight},
		{"milliseconds", "datetime", "UTC", midnight.UnixMilli(), midnight},
		{"float milliseconds", "datetime", "UTC", float64(midnight.UnixMilli()), midnight},
		{"JSON milliseconds", "datetime", "UTC", json.Number("1792339200000"), midnight},
		{"string milliseconds", "datetime", "UTC", "1792339200000", midnight},
		{"native time", "datetime", "UTC", midnight, midnight},
		{"epoch zero", "datetime", "UTC", 0, time.UnixMilli(0)},
		{"negative epoch", "datetime", "UTC", -1, time.UnixMilli(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTemporalValue(tc.fieldType, tc.value, tc.zone)
			require.NoError(t, err)
			assert.True(t, tc.want.Equal(got.(time.Time)), "want %v, got %v", tc.want, got)
			assert.Equal(t, tc.want.Nanosecond(), got.(time.Time).Nanosecond())
		})
	}
	for _, value := range []any{"2026-10-19T00:00:00+24:00", "2026-10-19T00:00:00+08:60", "2026-10-19T00:00:00.1234567891Z", json.Number("9007199254740993.0"), "9223372036854775808", "2026-10-19", "2026-02-30 00:00:00", "2026-10-19T00:00:00", "", nil, true, 1.5, math.Inf(1), math.NaN(), uint64(math.MaxUint64), []any{1}, json.Number("9223372036854775808")} {
		got, err := ParseTemporalValue(interfaces.DataType_Datetime, value, "UTC")
		require.Error(t, err, "value: %v", value)
		if got != nil {
			assert.True(t, got.(time.Time).IsZero())
		}
		assert.Contains(t, err.Error(), "invalid datetime")
	}
	_, gapErr := ParseTemporalValue(interfaces.DataType_Datetime, "2026-03-08 02:30:00", "America/New_York")
	require.ErrorContains(t, gapErr, "invalid datetime")

	_, err := ParseTemporalValue(interfaces.DataType_Datetime, "2026-10-19 00:00:00", "invalid/zone")
	require.ErrorContains(t, err, "server time zone")
}

func TestValidateHavingTemporalValues(t *testing.T) {
	t.Run("validate formats and timezone", func(t *testing.T) {
		t.Setenv("TZ", "Asia/Shanghai")
		field := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime}
		fields := map[string]*interfaces.Property{"created": field}
		params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "created", Aggr: "min"}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: "2026-10-19"}}
		require.ErrorContains(t, ValidateHavingTemporalValues(params, fields), "invalid datetime")
		params.Having.Value = []any{"2026-10-19 00:00:00", "2026-10-19"}
		require.ErrorContains(t, ValidateHavingTemporalValues(params, fields), "invalid datetime")
		params.Having.Value = "2026-10-19 00:00:00.123456789"
		require.NoError(t, ValidateHavingTemporalValues(params, fields))
		assert.Equal(t, "2026-10-19 00:00:00.123456789", params.Having.Value)
		t.Setenv("TZ", "invalid/zone")
		var zoneErr *interfaces.ServerTimeZoneError
		require.ErrorAs(t, ValidateHavingTemporalValues(params, fields), &zoneErr)
	})

	fields := map[string]*interfaces.Property{"created": {Name: "created", Type: interfaces.DataType_Datetime}}
	for _, aggr := range []string{"min", "max"} {
		params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "created", Aggr: aggr}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: "2026-10-19T00:00:00.1234567891Z"}}
		require.ErrorContains(t, ValidateHavingTemporalValues(params, fields), "9 fractional digits")
		params.Having.Value = []any{"2026-10-19T00:00:00.123Z", "2026-10-19T00:00:00.0000000000Z"}
		require.ErrorContains(t, ValidateHavingTemporalValues(params, fields), "9 fractional digits")
		params.Having.Value = "2026-10-19T00:00:00.123456789Z"
		require.NoError(t, ValidateHavingTemporalValues(params, fields))
		params.Having.Field = "count(*)"
		params.Having.Value = 1.123456
		require.NoError(t, ValidateHavingTemporalValues(params, fields))
	}
	require.NoError(t, ValidateHavingTemporalValues(&interfaces.ResourceDataQueryParams{}, fields))
}
