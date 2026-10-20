// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import "time"

const (
	// RFC3339FixedNanosLayout 固定输出 9 位小数秒，避免日期范围解析补齐缺失精度。
	RFC3339FixedNanosLayout = "2006-01-02T15:04:05.000000000Z07:00"
	// TimeLayout 保留最多 9 位小数秒，省略尾部零。
	TimeLayout = time.TimeOnly + ".999999999"
	// TimeFixedNanosLayout 固定输出 9 位小数秒，用于 keyword 时间范围的上边界。
	TimeFixedNanosLayout = time.TimeOnly + ".000000000"
	// DatetimeLayout 表示无时区日期时间，保留最多 9 位小数秒。
	DatetimeLayout = time.DateTime + ".999999999"
	// Time100nsLayout 用于 SQL Server/HANA 的 100ns 精度时间绑定或输出。
	Time100nsLayout = time.TimeOnly + ".9999999"
	// ISO8601Datetime100nsLayout 用于 SQL Server 无时区列的 ISO 8601 文本绑定。
	ISO8601Datetime100nsLayout = "2006-01-02T15:04:05.9999999"
)
