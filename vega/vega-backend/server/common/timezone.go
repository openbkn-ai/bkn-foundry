// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"os"
	"strings"
)

// ServerTimeZone 返回服务日历计算及无时区输入使用的时区；未配置时使用 UTC。
func ServerTimeZone() string {
	if zone := strings.TrimSpace(os.Getenv("TZ")); zone != "" {
		return zone
	}
	return "UTC"
}
