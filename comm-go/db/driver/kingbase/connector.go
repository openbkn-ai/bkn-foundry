// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package kingbase

import (
	"context"
	"database/sql/driver"
)

type KBConnector struct {
	driver.Connector
}

func (kbConnector *KBConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := kbConnector.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &KBConn{conn}, err
}

func (kbConnector *KBConnector) Driver() driver.Driver {
	return Driver{}
}
