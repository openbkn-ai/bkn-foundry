// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE for the full text.

package interfaces

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDataType_HasDatePart(t *testing.T) {
	for _, dataType := range []string{DataType_Date, DataType_Datetime, DataType_Timestamp} {
		t.Run(dataType, func(t *testing.T) {
			assert.True(t, DataType_HasDatePart(dataType))
		})
	}
	for _, dataType := range []string{
		DataType_Time, DataType_Integer, DataType_UnsignedInteger, DataType_LargeInteger,
		DataType_Float, DataType_Decimal, DataType_String, DataType_Text, DataType_Ip,
		DataType_Boolean, DataType_Binary, DataType_Json, DataType_Point, DataType_Shape,
		DataType_Vector, DataType_Other, "unknown",
	} {
		t.Run(dataType, func(t *testing.T) {
			assert.False(t, DataType_HasDatePart(dataType))
		})
	}
	// 原有宽泛日期族判断继续包含 time。
	assert.True(t, DataType_IsDate(DataType_Time))
}
