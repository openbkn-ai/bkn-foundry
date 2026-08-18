// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"database/sql"
	"fmt"
)

const minimumPostgresqlServerVersionNumber = 90200

type postgresqlCompatibility struct {
	serverVersionNum int
	checked          bool
}

func fetchPostgresqlCompatibility(ctx context.Context, db *sql.DB) (postgresqlCompatibility, error) {
	var compatibility postgresqlCompatibility
	if err := db.QueryRowContext(ctx, "SHOW server_version_num").
		Scan(&compatibility.serverVersionNum); err != nil {
		return postgresqlCompatibility{}, err
	}
	compatibility.checked = true
	return compatibility, nil
}

func (c postgresqlCompatibility) validateMinimum() error {
	if c.serverVersionNum < minimumPostgresqlServerVersionNumber {
		return fmt.Errorf(
			"PostgreSQL %s is not supported; require PostgreSQL 9.2+",
			postgresqlVersion(c.serverVersionNum),
		)
	}
	return nil
}

func (c postgresqlCompatibility) supportsLateral() bool {
	return !c.checked || c.serverVersionNum >= 90300
}

func (c postgresqlCompatibility) supportsWithOrdinality() bool {
	return !c.checked || c.serverVersionNum >= 90400
}

// tableRelKinds returns relation kinds supported by the connected PostgreSQL version:
//   - r: ordinary table
//   - v: view
//   - f: foreign table
//   - m: materialized view (PostgreSQL 9.3+)
//   - p: partitioned table (PostgreSQL 10+)
//
// An unchecked version keeps all kinds for connectors constructed directly in tests
// or legacy call paths.
func (c postgresqlCompatibility) tableRelKinds() []string {
	relKinds := []string{"r", "v", "f"}
	if !c.checked || c.serverVersionNum >= 90300 {
		relKinds = append(relKinds, "m")
	}
	if !c.checked || c.serverVersionNum >= 100000 {
		relKinds = append(relKinds, "p")
	}
	return relKinds
}

func postgresqlVersion(serverVersionNum int) string {
	major := serverVersionNum / 10000
	if major >= 10 {
		return fmt.Sprintf("%d.%d", major, serverVersionNum%10000)
	}

	minor := (serverVersionNum / 100) % 100
	patch := serverVersionNum % 100
	if patch == 0 {
		return fmt.Sprintf("%d.%d", major, minor)
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}
