// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeBuildTemporalValue(t *testing.T) {
	for _, tc := range []struct {
		name, fieldType, logicalType string
		input, want                  any
	}{
		{"source calendar day", interfaces.DataType_Date, "", time.Date(2026, 10, 19, 0, 0, 0, 0, time.FixedZone("test", 8*3600)), "2026-10-19"},
		{"CDC days", interfaces.DataType_Date, "io.debezium.time.Date", json.Number("20745"), "2026-10-19"},
		{"Connect days", interfaces.DataType_Date, "org.apache.kafka.connect.data.Date", json.Number("-1"), "1969-12-31"},
		{"unknown date unit", interfaces.DataType_Date, "", json.Number("20745"), json.Number("20745")},
		{"milliseconds", interfaces.DataType_Time, "io.debezium.time.Time", json.Number("43200123"), "12:00:00.123"},
		{"Connect milliseconds", interfaces.DataType_Time, "org.apache.kafka.connect.data.Time", json.Number("43200123"), "12:00:00.123"},
		{"microseconds", interfaces.DataType_Time, "io.debezium.time.MicroTime", json.Number("43200123456"), "12:00:00.123456"},
		{"nanoseconds", interfaces.DataType_Time, "io.debezium.time.NanoTime", json.Number("43200123456789"), "12:00:00.123456789"},
		{"last microsecond", interfaces.DataType_Time, "io.debezium.time.MicroTime", json.Number("86399999999"), "23:59:59.999999"},
		{"zero", interfaces.DataType_Time, "io.debezium.time.MicroTime", json.Number("0"), "00:00:00"},
		{"unknown time unit", interfaces.DataType_Time, "unknown", json.Number("43200123456"), json.Number("43200123456")},
		{"negative duration", interfaces.DataType_Time, "io.debezium.time.MicroTime", json.Number("-3600000000"), json.Number("-3600000000")},
		{"over day duration", interfaces.DataType_Time, "io.debezium.time.MicroTime", json.Number("86400000000"), json.Number("86400000000")},
		{"unsupported source precision", interfaces.DataType_Time, "", "12:00:00.1234567891", "12:00:00.1234567891"},
		{"long source time", interfaces.DataType_Time, "", "100:00:00", "100:00:00"},
		{"negative source time", interfaces.DataType_Time, "", "-01:02:03.400000", "-01:02:03.400000"},
		{"calendar bytes", interfaces.DataType_Date, "", []byte("2026-10-19"), "2026-10-19"},
		{"serialized native clock", interfaces.DataType_Time, "", "0000-01-01T12:00:00.100000+08:00", "12:00:00.1"},
		{"noninteger", interfaces.DataType_Time, "io.debezium.time.MicroTime", json.Number("1.5"), json.Number("1.5")},
		{"overflow", interfaces.DataType_Time, "io.debezium.time.MicroTime", json.Number("9223372036854775808"), json.Number("9223372036854775808")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeBuildTemporalValue(tc.fieldType, tc.input, tc.logicalType))
		})
	}
}
