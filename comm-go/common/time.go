// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"fmt"
	"time"
)

const (
	// 毫秒时间字符串
	RFC3339Milli = "2006-01-02T15:04:05.999Z07:00"
)

/*
	AnyRobot希望后端API统一时间输出格式为RFC3339Milli.
	Golang的标准库time.Time的输出格式因值的不同会存在差异, 可能是RFC3339/RFC3339Nano/其它.
	因此我们定义Time类型, 覆写MarshalJSON, UnmarshalJSON等方法, 实现期望效果.
*/

type Time time.Time

func Now() Time {
	return Time(time.Now())
}

func (t Time) MarshalJSON() ([]byte, error) {
	stamp := fmt.Sprintf("\"%s\"", time.Time(t).Format(RFC3339Milli))
	return []byte(stamp), nil
}

func (t *Time) UnmarshalJSON(b []byte) error {
	st := time.Time{}
	err := st.UnmarshalJSON(b)
	if err != nil {
		return err
	}

	*t = Time(st)
	return nil
}

func (t Time) String() string {
	return time.Time(t).String()
}

func (t Time) Add(d time.Duration) Time {
	return Time(time.Time(t).Add(d))
}

func (t Time) After(u Time) bool {
	return time.Time(t).After(time.Time(u))
}

func (t Time) Before(u Time) bool {
	return time.Time(t).Before(time.Time(u))
}

func (t Time) Sub(u Time) time.Duration {
	return time.Time(t).Sub(time.Time(u))
}
