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

	"github.com/lib/pq"
	"github.com/lib/pq/pqerror"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

const (
	postgresqlLateralProbe = `SELECT 1
FROM (SELECT ARRAY[1] AS items) AS base
JOIN LATERAL generate_subscripts(base.items, 1) AS probe(position) ON true`

	postgresqlWithOrdinalityProbe = `SELECT ordinal_position
FROM (SELECT ARRAY[1] AS items) AS base
JOIN LATERAL unnest(base.items) WITH ORDINALITY AS probe(value, ordinal_position) ON true`

	// numeric 的文本输出保留 money 的小数位；length/split_part 兼容不提供 scale(numeric) 的旧版本。
	postgresqlMoneyScaleProbe = "SELECT pg_catalog.length(pg_catalog.split_part(CAST(CAST(CAST(0 AS money) AS numeric) AS text), '.', 2))"
)

type postgresqlCompatibility struct {
	lateral        bool
	withOrdinality bool

	// money 定义在连接初始化时探测，后续只读；零 scale 也是有效结果。
	moneyScale sql.NullInt64
}

// detectPostgresqlCompatibility probes metadata-query features supported by the connected database.
func detectPostgresqlCompatibility(ctx context.Context, db *sql.DB) (postgresqlCompatibility, error) {
	var compatibility postgresqlCompatibility
	var probeResult int
	if err := db.QueryRowContext(ctx, postgresqlLateralProbe).Scan(&probeResult); err != nil {
		// 固定能力探针仅对不支持的语法或函数降级，其他错误中止初始化。
		if pq.As(err, pqerror.FeatureNotSupported, pqerror.SyntaxError, pqerror.UndefinedFunction) == nil {
			return postgresqlCompatibility{}, fmt.Errorf("failed to detect PostgreSQL LATERAL support: %w", err)
		}
		logger.Warnf("Failed to detect PostgreSQL LATERAL support, using compatible metadata query: %v", connectionError(ctx, "capability probe failed", err))
	} else {
		compatibility.lateral = true
	}
	if err := db.QueryRowContext(ctx, postgresqlWithOrdinalityProbe).Scan(&probeResult); err != nil {
		if pq.As(err, pqerror.FeatureNotSupported, pqerror.SyntaxError, pqerror.UndefinedFunction) == nil {
			return postgresqlCompatibility{}, fmt.Errorf("failed to detect PostgreSQL WITH ORDINALITY support: %w", err)
		}
		logger.Warnf("Failed to detect PostgreSQL WITH ORDINALITY support, using compatible metadata query: %v", connectionError(ctx, "capability probe failed", err))
	} else {
		compatibility.withOrdinality = true
	}
	if err := db.QueryRowContext(ctx, postgresqlMoneyScaleProbe).Scan(&compatibility.moneyScale); err != nil {
		return postgresqlCompatibility{}, fmt.Errorf("failed to read PostgreSQL money scale: %w", err)
	} else if !compatibility.moneyScale.Valid || compatibility.moneyScale.Int64 < 0 || compatibility.moneyScale.Int64 > 10 {
		return postgresqlCompatibility{}, fmt.Errorf("invalid PostgreSQL money scale")
	}
	return compatibility, nil
}

// supportsLateral reports whether LATERAL metadata queries are supported.
func (c postgresqlCompatibility) supportsLateral() bool {
	return c.lateral
}

// supportsWithOrdinality reports whether WITH ORDINALITY metadata queries are supported.
func (c postgresqlCompatibility) supportsWithOrdinality() bool {
	return c.withOrdinality
}
