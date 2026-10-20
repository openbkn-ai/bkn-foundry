// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"encoding/json"
	"math"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

// normalizeBuildTemporalValue 只规范可识别的源端日期/日内时间。
// MySQL 的有符号时长、超 24 小时 TIME 以及未知表示保留源值，不能当作非法 HTTP 常量拒绝构建。
func normalizeBuildTemporalValue(fieldType string, value any, logicalType string) any {
	layout := time.DateOnly
	if fieldType == interfaces.DataType_Time {
		layout = common.TimeLayout
	}
	if native, ok := value.(time.Time); ok {
		return native.Format(layout)
	}
	if raw, ok := value.([]byte); ok {
		value = string(raw)
	}
	if text, ok := value.(string); ok {
		// 源值超出 Go 时间精度时保留原文，不能让 time.Parse 静默截断。
		if err := filter_condition.ValidateTemporalPrecision(fieldType, text); err != nil {
			return value
		}
		if native, err := time.Parse(layout, text); err == nil {
			return native.Format(layout)
		}
		if native, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return native.Format(layout)
		}
		return value
	}
	var number int64
	var ok bool
	if precise, preciseNumber := value.(json.Number); preciseNumber {
		var err error
		number, err = precise.Int64()
		ok = err == nil
	} else {
		number, ok = common.NumberAsInt64(value)
	}
	if !ok {
		return value
	}
	if fieldType == interfaces.DataType_Date {
		if (logicalType == "io.debezium.time.Date" || logicalType == "org.apache.kafka.connect.data.Date") && number >= math.MinInt64/86400 && number <= math.MaxInt64/86400 {
			day := time.Unix(number*86400, 0).UTC().Format(time.DateOnly)
			if len(day) == len(time.DateOnly) {
				return day
			}
		}
		return value
	}
	var unit time.Duration
	switch logicalType {
	case "io.debezium.time.Time", "org.apache.kafka.connect.data.Time":
		unit = time.Millisecond
	case "io.debezium.time.MicroTime":
		unit = time.Microsecond
	case "io.debezium.time.NanoTime":
		unit = time.Nanosecond
	}
	if unit != 0 && number >= 0 && number < int64(24*time.Hour/unit) {
		return time.Unix(0, number*int64(unit)).UTC().Format(common.TimeLayout)
	}
	return value
}
