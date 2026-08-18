// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	minimumMySQLServerVersionNumber   = 50700
	minimumMariaDBServerVersionNumber = 100500
)

type mariadbProduct string

const (
	mariadbProductMySQL   mariadbProduct = "MySQL"
	mariadbProductMariaDB mariadbProduct = "MariaDB"
)

var databaseVersionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

type mariadbCompatibility struct {
	product          mariadbProduct
	serverVersionNum int
	checked          bool
}

func fetchMariaDBCompatibility(ctx context.Context, db *sql.DB) (mariadbCompatibility, error) {
	var version, versionComment sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT VERSION(), @@version_comment").
		Scan(&version, &versionComment); err != nil {
		return mariadbCompatibility{}, err
	}
	if !version.Valid || !versionComment.Valid {
		return mariadbCompatibility{}, fmt.Errorf("required database version metadata contains NULL")
	}
	return parseMariaDBCompatibility(version.String, versionComment.String)
}

func parseMariaDBCompatibility(version, versionComment string) (mariadbCompatibility, error) {
	product := mariadbProductMySQL
	versionSource := version
	if strings.Contains(strings.ToLower(version+" "+versionComment), "mariadb") {
		product = mariadbProductMariaDB
		if marker := strings.Index(strings.ToLower(versionSource), "mariadb"); marker >= 0 {
			versionSource = versionSource[:marker]
		}
	}

	matches := databaseVersionPattern.FindAllString(versionSource, -1)
	if len(matches) == 0 {
		return mariadbCompatibility{}, fmt.Errorf("failed to parse database version %q", version)
	}
	versionValue := matches[0]
	if product == mariadbProductMariaDB {
		versionValue = matches[len(matches)-1]
	}
	serverVersionNum, err := mariaDBVersionNumber(versionValue)
	if err != nil {
		return mariadbCompatibility{}, fmt.Errorf("failed to parse database version %q: %w", version, err)
	}

	return mariadbCompatibility{
		product:          product,
		serverVersionNum: serverVersionNum,
		checked:          true,
	}, nil
}

func mariaDBVersionNumber(version string) (int, error) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid version %q", version)
	}
	values := make([]int, 3)
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return 0, err
		}
		values[i] = value
	}
	return values[0]*10000 + values[1]*100 + values[2], nil
}

func (c mariadbCompatibility) validateMinimum() error {
	minimumVersion := minimumMySQLServerVersionNumber
	minimumVersionText := "5.7"
	if c.product == mariadbProductMariaDB {
		minimumVersion = minimumMariaDBServerVersionNumber
		minimumVersionText = "10.5"
	}
	if c.serverVersionNum < minimumVersion {
		return fmt.Errorf(
			"%s %s is not supported; require %s %s+",
			c.product,
			mariaDBVersion(c.serverVersionNum),
			c.product,
			minimumVersionText,
		)
	}
	return nil
}

func mariaDBVersion(serverVersionNum int) string {
	major := serverVersionNum / 10000
	minor := (serverVersionNum / 100) % 100
	patch := serverVersionNum % 100
	if patch == 0 {
		return fmt.Sprintf("%d.%d", major, minor)
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}
