package table

import (
	"os"
	"strings"
)

// ServerTimeZone 返回日历分桶使用的服务时区。
func ServerTimeZone() string {
	if timeZone := strings.TrimSpace(os.Getenv("TZ")); timeZone != "" {
		return timeZone
	}
	return "UTC"
}
