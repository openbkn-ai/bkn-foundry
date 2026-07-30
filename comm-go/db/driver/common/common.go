// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"errors"
	"strings"

	"github.com/emirpasic/gods/maps/treemap"
	"github.com/go-sql-driver/mysql"
)

var (
	ErrInvalidDSNFormat = errors.New("invalid DSN: invalidFormat")
)

type DSNConfig struct {
	Username string
	Password string
	Host     string
	Port     string
	Protocol string
	DBName   string
	Props    *treemap.Map
}

func ParseMySQLDSN(dsn string) (mysql.Config, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return mysql.Config{}, err
	}
	return *cfg, err
}

// ParseDSN 解析DSN字符串，返回DSN参数映射，参数保持原大小写
// DSN格式：user:password@protocol(host:port)/dbname?param=value&param=value
func ParseDSN(dsn string) (DSNConfig, error) {

	if dsn == "" {
		return DSNConfig{}, ErrInvalidDSNFormat
	}

	dsnConfig := DSNConfig{
		Props: treemap.NewWithStringComparator(),
	}
	urlString := dsn
	// urlString格式: user:password@protocol(host:port)/dbname?param=value&param=value
	if queryIndex := strings.LastIndex(dsn, "?"); queryIndex > 0 {
		urlString = dsn[:queryIndex]
		// urlString格式: user:password@protocol(host:port)/dbname
		for _, kvString := range strings.Split(dsn[queryIndex+1:], "&") {
			kv := strings.SplitN(kvString, "=", 2)
			if len(kv) > 1 {
				dsnConfig.Props.Put(kv[0], kv[1])
			}
		}
	}

	atIndex := strings.LastIndex(urlString, "@")
	if atIndex == -1 {
		return DSNConfig{}, ErrInvalidDSNFormat
	}
	userString := urlString[:atIndex]
	hostString := urlString[atIndex+1:]
	// userString格式: user:password
	// hostString格式: protocol(host:port)/dbname

	if kv := strings.SplitN(userString, ":", 2); len(kv) > 1 {
		dsnConfig.Username = kv[0]
		dsnConfig.Password = kv[1]
	} else {
		dsnConfig.Username = userString
	}

	if schemaIndex := strings.LastIndex(hostString, "/"); schemaIndex > 0 {
		dsnConfig.DBName = hostString[schemaIndex+1:]
		hostString = hostString[:schemaIndex]
		// hostString格式: protocol(host:port)
	}

	if sIdx := strings.Index(hostString, "("); sIdx > 0 {
		if !strings.HasSuffix(hostString, ")") {
			return DSNConfig{}, ErrInvalidDSNFormat
		}
		eIdx := len(hostString) - 1
		dsnConfig.Protocol = hostString[:sIdx]
		hostString = hostString[sIdx+1 : eIdx]
		// hostString格式: host:port
	}

	if strings.HasPrefix(hostString, "[") {
		bracketIdx := strings.Index(hostString, "]")
		if bracketIdx == -1 {
			return DSNConfig{}, ErrInvalidDSNFormat
		}
		if bracketIdx == len(hostString)-1 {
			dsnConfig.Host = hostString
		} else if hostString[bracketIdx+1] == ':' {
			dsnConfig.Host = hostString[:bracketIdx+1]
			dsnConfig.Port = hostString[bracketIdx+2:]
		} else {
			dsnConfig.Host = hostString
		}
	} else if idx := strings.LastIndex(hostString, ":"); idx > 0 {
		dsnConfig.Host = hostString[:idx]
		dsnConfig.Port = hostString[idx+1:]
	} else {
		dsnConfig.Host = hostString
	}

	return dsnConfig, nil
}
