// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package kingbase

import (
	"database/sql/driver"

	"github.com/openbkn-ai/bkn-comm-go/db/driver/common"
	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb"
)

func Open(dsn string) (driver.Conn, error) {
	cfg, err := common.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	newDSN, err := FormatDSN(cfg)
	if err != nil {
		return nil, err
	}
	conn, err := gokb.Open(newDSN)
	if err != nil {
		return nil, err
	}
	return KBConn{Conn: conn}, err
}

func OpenConnector(dsn string) (driver.Connector, error) {
	cfg, err := common.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	newDSN, err := FormatDSN(cfg)
	if err != nil {
		return nil, err
	}
	cnct, err := gokb.NewConnector(newDSN)
	if err != nil {
		return nil, err
	}
	return &KBConnector{Connector: cnct}, err
}
