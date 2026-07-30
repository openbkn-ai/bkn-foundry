// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dmdb

import (
	"database/sql/driver"

	"gitee.com/chunanyong/dm"

	"github.com/openbkn-ai/bkn-comm-go/db/driver/common"
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
	dmConn, err := (&dm.DmDriver{}).Open(newDSN)
	if err != nil {
		return nil, err
	}
	return &DMConn{dmConn}, err
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
	dmConnector, err := (&dm.DmDriver{}).OpenConnector(newDSN)
	if err != nil {
		return nil, err
	}
	return &DMConnector{dmConnector}, err
}
