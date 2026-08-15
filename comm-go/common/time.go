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
	// RFC3339 timestamp with millisecond precision.
	RFC3339Milli = "2006-01-02T15:04:05.999Z07:00"
)

/*
Time provides a stable RFC3339Milli JSON representation for backend APIs.
time.Time can otherwise serialize with RFC3339 or RFC3339Nano precision
depending on its value, so this type controls JSON marshaling explicitly.
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
