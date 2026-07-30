// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dmdb

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/db/driver/common"
)

var (
	REPLACE_PARAMS = map[string]string{
		"timeout":    "connectTimeout",
		"autocommit": "autoCommit",
	}
)

var (
	errNoDMSVCConf = errors.New("invalid DMSVCConf: no dm_svc_conf,may permission problem?please check env")
)

func FormatDSN(cfg common.DSNConfig) (string, error) {
	if strings.Contains(cfg.Host, ",") {
		err := customDMSVCConf(cfg.Host, cfg.Port)
		if err != nil {
			return "", errNoDMSVCConf
		}
		cfg.Host = "DM"
	}

	for param, replaceParam := range REPLACE_PARAMS {
		if value, exist := cfg.Props.Get(param); exist {
			switch param {
			case "timeout":
				t, err := time.ParseDuration(value.(string))
				if err != nil {
					return "", err
				}
				value = strconv.FormatInt(t.Milliseconds(), 10)
			}
			cfg.Props.Put(replaceParam, value)
			cfg.Props.Remove(param)
			continue
		}
	}
	cfg.Props.Put("compatibleMode", "mysql")
	cfg.Props.Put("escapeProcess", "true")
	cfg.Props.Put("svcConfPath", "/tmp/dm_svc.conf")

	hostPort := ""
	if cfg.Port != "" {
		hostPort = fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
	} else {
		hostPort = cfg.Host
	}
	dsn := fmt.Sprintf("dm://%s:%s@%s?schema=%s", cfg.Username, cfg.Password, hostPort, cfg.DBName)
	for _, param := range cfg.Props.Keys() {
		value, _ := cfg.Props.Get(param)
		dsn = fmt.Sprintf("%s&%s=%s", dsn, param, value.(string))
	}
	return dsn, nil
}

func customDMSVCConf(host string, port string) error {
	var result []string

	if strings.HasPrefix(host, "[") {
		if !strings.HasSuffix(host, "]") {
			return common.ErrInvalidDSNFormat
		}
		host = host[1 : len(host)-1]

		ips := strings.Split(host, ",")
		for _, ip := range ips {
			trimmedIP := strings.TrimSpace(ip)
			if trimmedIP == "" {
				continue
			}
			result = append(result, fmt.Sprintf("[%s]:%s", trimmedIP, port))
		}
	} else {
		ips := strings.Split(host, ",")
		for _, ip := range ips {
			trimmedIP := strings.TrimSpace(ip)
			if trimmedIP == "" {
				continue
			}
			result = append(result, fmt.Sprintf("%s:%s", trimmedIP, port))
		}
	}

	dmsvc := fmt.Sprintf("DM=(%s)", strings.Join(result, ","))
	file, err := os.Create("/tmp/dm_svc.conf")
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(dmsvc)
	if err != nil {
		return err
	}
	return nil
}
