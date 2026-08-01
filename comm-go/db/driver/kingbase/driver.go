// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package kingbase

import "database/sql/driver"

type Driver struct{}

func (d Driver) Open(name string) (driver.Conn, error) {
	return Open(name)
}
