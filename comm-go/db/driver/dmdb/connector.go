// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dmdb

import (
	"context"
	"database/sql/driver"
)

type DMConnector struct {
	driver.Connector
}

func (dmConnector *DMConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := dmConnector.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &DMConn{conn}, err
}
