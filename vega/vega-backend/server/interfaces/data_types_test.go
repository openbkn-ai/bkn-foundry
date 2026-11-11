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

func TestPropertyEffectiveFilterable(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want bool
	}{
		{DataType_Integer, true}, {DataType_UnsignedInteger, true}, {DataType_LargeInteger, true},
		{DataType_Float, true}, {DataType_Decimal, true}, {DataType_String, true}, {DataType_Text, true},
		{DataType_Date, true}, {DataType_Time, true}, {DataType_Datetime, true}, {DataType_Timestamp, true},
		{DataType_Ip, true}, {DataType_Boolean, true}, {DataType_Vector, true},
		{DataType_Binary, false}, {DataType_Json, false}, {DataType_Other, false},
		{DataType_Point, false}, {DataType_Shape, false}, {"future_type", false}, {"", false},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			property := &Property{Type: tc.kind}
			assert.Equal(t, tc.want, property.EffectiveFilterable())
			assert.Nil(t, property.Filterable, "读取历史缺失值时不得修改原字段")
			property.SetDefaultFilterable()
			assert.Equal(t, tc.want, *property.Filterable)
			for _, explicit := range []bool{false, true} {
				property.Filterable = &explicit
				property.SetDefaultFilterable()
				assert.Equal(t, explicit, property.EffectiveFilterable())
			}
		})
	}
}
