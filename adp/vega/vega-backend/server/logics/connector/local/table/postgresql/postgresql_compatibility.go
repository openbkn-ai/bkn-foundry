// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

const (
	postgresqlLateralProbe = `SELECT 1
FROM (SELECT ARRAY[1] AS items) AS base
JOIN LATERAL generate_subscripts(base.items, 1) AS probe(position) ON true`

	postgresqlWithOrdinalityProbe = `SELECT ordinal_position
FROM (SELECT ARRAY[1] AS items) AS base
JOIN LATERAL unnest(base.items) WITH ORDINALITY AS probe(value, ordinal_position) ON true`
)

type postgresqlCompatibility struct {
	lateral        bool
	withOrdinality bool
}

func detectPostgresqlCompatibility(ctx context.Context, db *sql.DB) postgresqlCompatibility {
	var compatibility postgresqlCompatibility
	var probeResult int
	if err := db.QueryRowContext(ctx, postgresqlLateralProbe).Scan(&probeResult); err != nil {
		logger.Warnf("Failed to detect PostgreSQL LATERAL support, using compatible metadata query: %v", err)
	} else {
		compatibility.lateral = true
	}
	if err := db.QueryRowContext(ctx, postgresqlWithOrdinalityProbe).Scan(&probeResult); err != nil {
		logger.Warnf("Failed to detect PostgreSQL WITH ORDINALITY support, using compatible metadata query: %v", err)
	} else {
		compatibility.withOrdinality = true
	}
	return compatibility
}

func (c postgresqlCompatibility) supportsLateral() bool {
	return c.lateral
}

func (c postgresqlCompatibility) supportsWithOrdinality() bool {
	return c.withOrdinality
}
