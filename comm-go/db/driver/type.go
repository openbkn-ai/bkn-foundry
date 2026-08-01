// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driver

import (
	"database/sql/driver"
	"fmt"
	"time"
)

type Time struct {
	time.Time
}

func (T *Time) Scan(value interface{}) error {
	switch v := value.(type) {
	case time.Time:
		T.Time = v
	case []byte:
		for _, layout := range []string{
			"2006-01-02T15:04:05Z07:00",
			"2006-01-02T15:04:05.999999999Z07:00",
			"2006-01-02 15:04:05",
			"2006-01-02",
			"15:04:05",
		} {
			t, err := time.ParseInLocation(layout, string(v), time.Local)
			if err == nil {
				T.Time = t
				return nil
			}
		}
		//T.t = time.Parse(time.RFC3339, string(v))
		return fmt.Errorf("parse %s is unsupported", string(v))
	default:
		fmt.Println(v)
	}
	return nil
}

func (T Time) Value() (driver.Value, error) {
	return T.Time, nil
}
