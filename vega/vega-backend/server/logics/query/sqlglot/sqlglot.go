// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package sqlglot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// GenericDialect is SQLGlot's empty dialect, which emits generic SQL.
const GenericDialect = ""

// ExtractTablesResult stores the extraction results of the table
type ExtractTablesResult struct {
	Tables  []*Table `json:"tables"`
	SQL     string   `json:"sql"`
	Dialect string   `json:"dialect"`
	Error   string   `json:"error"`
}

type Table struct {
	Catalog string `json:"catalog"`
	Schema  string `json:"schema"`
	Name    string `json:"name"`
}

// SQLParseResult stores the results of SQL parsing
type SQLParseResult struct {
	AST     interface{} `json:"ast"`
	SQL     string      `json:"sql"`
	Dialect string      `json:"dialect"`
	Error   string      `json:"error"`
}

// ExtractTables extracts all table names from SQL
func ExtractTables(sql string, dialect string) (*ExtractTablesResult, error) {
	//nolint:gosec // Static script; untrusted values are argv, not shell input.
	cmd := exec.Command("python3", "-c", `
import sys
import json
import sqlglot
from sqlglot.expressions import Table

try:
    sql = sys.argv[1]
    dialect = sys.argv[2]
    tables = [{
        "catalog": t.catalog,
        "schema": t.db,
        "name": t.name
    } for t in sqlglot.parse_one(sql, dialect=dialect).find_all(Table)]
    print(json.dumps({
        "tables": tables,
        "sql": sql,
        "dialect": dialect,
        "error": None
    }))
except Exception as e:
    print(json.dumps({
        "tables": [],
        "sql": sql,
        "dialect": dialect,
        "error": str(e)
    }))
`, sql, dialect)

	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		logger.Errorf("ExtractTables command failed")
		return nil, err
	}

	var result ExtractTablesResult
	if err := sonic.Unmarshal(out.Bytes(), &result); err != nil {
		logger.Errorf("ExtractTables response decode failed")
		return nil, err
	}

	if result.Error != "" {
		logger.Errorf("ExtractTables rejected the SQL input")
		return nil, errors.New(result.Error)
	}

	return &result, nil
}

// MapDataSourceTypeToDialect mapped to the data source type sqlglot dialect
func MapDataSourceTypeToDialect(dataSourceType string) (string, error) {
	switch strings.ToLower(dataSourceType) {
	case interfaces.ConnectorTypeMySQL:
		return "mysql", nil
	case "postgres", interfaces.ConnectorTypePostgreSQL:
		return "postgres", nil
	case "maria", interfaces.ConnectorTypeMariaDB:
		return "mysql", nil // MariaDB uses the mysql dialect
	case "tsql", interfaces.ConnectorTypeSQLServer:
		return "tsql", nil
	case interfaces.ConnectorTypeOracle:
		return "oracle", nil
	case interfaces.ConnectorTypeHANA, GenericDialect:
		return GenericDialect, nil
	default:
		logger.Errorf("unsupported dataSourceType: %s", dataSourceType)
		return "", fmt.Errorf("unsupported dataSourceType: %s", dataSourceType)
	}
}

// TranspileSQL 转换 SQL 方言；SQL Server 目标（含同方言编译）的非 ASCII
// 字符串字面量按 Unicode 保留，ASCII 字面量保留 varchar 类型。
func TranspileSQL(ctx context.Context, sql string, fromDialect string, dataSourceType string) (*SQLParseResult, error) {

	// Map the data source type to the sqlglot dialect
	toDialect, err := MapDataSourceTypeToDialect(dataSourceType)
	if err != nil {
		logger.Errorf("MapDataSourceTypeToDialect failed, %s", err.Error())
		return nil, err
	}

	//nolint:gosec // Static script; untrusted values are argv, not shell input.
	cmd := exec.CommandContext(ctx, "python3", "-c", `
import sys
import json
import sqlglot
from sqlglot import exp

try:
    sql = sys.argv[1]
    from_dialect = sys.argv[2]
    to_dialect = sys.argv[3]
    if to_dialect == "tsql":
        def preserve_unicode(node):
            if isinstance(node, exp.Literal) and node.is_string and not node.this.isascii():
                national = exp.National(this=node.this)
                national.add_comments(node.comments)
                return national
            return node

        statement = sqlglot.parse_one(sql, read=from_dialect)
        transpiled = statement.transform(preserve_unicode).sql(dialect=to_dialect)
    else:
        transpiled = sqlglot.transpile(sql, read=from_dialect, write=to_dialect)[0]
    print(json.dumps({
        "ast": None,
        "sql": transpiled,
        "dialect": to_dialect,
        "error": None
    }))
except Exception as e:
    print(json.dumps({
        "ast": None,
        "sql": sql,
        "dialect": from_dialect,
        "error": str(e)
    }))
`, sql, fromDialect, toDialect)

	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		logger.Errorf("TranspileSQL command failed")
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	var result SQLParseResult
	if err := sonic.Unmarshal(out.Bytes(), &result); err != nil {
		logger.Errorf("TranspileSQL response decode failed")
		return nil, err
	}

	if result.Error != "" {
		logger.Errorf("TranspileSQL rejected the SQL input")
		return nil, errors.New(result.Error)
	}

	return &result, nil
}

// BindTableReferences replaces compiler-preserved temporary tables with trusted
// source references in the target AST, retaining table aliases and qualified columns.
func BindTableReferences(ctx context.Context, sql, dialect string, bindings map[string]string) (*SQLParseResult, error) {
	data, err := sonic.Marshal(bindings)
	if err != nil {
		return nil, err
	}
	//nolint:gosec // Static script; SQL and resource bindings are argv, not shell input.
	cmd := exec.CommandContext(ctx, "python3", "-c", `
import json
import sys
import sqlglot
from sqlglot import exp

try:
    sql, dialect, bindings = sys.argv[1], sys.argv[2], json.loads(sys.argv[3])
    sources = {}
    for marker, reference in bindings.items():
        parsed = sqlglot.parse("SELECT * FROM " + reference, read=dialect)
        if len(parsed) != 1 or not isinstance(parsed[0], exp.Select):
            raise ValueError("invalid source reference")
        statement = parsed[0]
        source = statement.args.get("from_") or statement.args.get("from")
        table = source.this if source is not None else None
        if not isinstance(table, exp.Table) or table.args.get("alias") is not None:
            raise ValueError("invalid source reference")
        if any(statement.args.get(key) for key in ("joins", "where", "group", "having", "order", "limit", "offset")):
            raise ValueError("invalid source reference")
        if not all(isinstance(part, exp.Identifier) for part in table.parts):
            raise ValueError("invalid source reference")
        sources[marker] = table
    statement = sqlglot.parse_one(sql, read=dialect)
    aliases = {}
    seen = set()
    for table in list(statement.find_all(exp.Table)):
        marker = table.name
        if marker not in sources or table.db or table.catalog:
            raise ValueError("unbound table")
        replacement = sources[marker].copy()
        if table.comments:
            replacement.add_comments(table.comments)
        alias = table.args.get("alias")
        if alias is not None:
            replacement.set("alias", alias.copy())
        aliases.setdefault(marker, []).append(alias)
        seen.add(marker)
        table.replace(replacement)
    if seen != set(bindings):
        raise ValueError("table binding lost during compilation")
    for column in statement.find_all(exp.Column):
        marker = column.table
        if marker not in sources:
            continue
        if column.db or column.catalog or len(aliases[marker]) != 1:
            raise ValueError("ambiguous resource column qualifier")
        alias = aliases[marker][0]
        if alias is not None:
            column.set("table", alias.this.copy())
        else:
            table = sources[marker]
            column.set("table", table.this.copy())
            for key in ("db", "catalog"):
                component = table.args.get(key)
                column.set(key, component.copy() if component is not None else None)
    print(json.dumps({"sql": statement.sql(dialect=dialect), "dialect": dialect, "error": ""}))
except Exception:
    print(json.dumps({"sql": "", "error": "invalid SQL resource binding"}))
`, sql, dialect, string(data))
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var result SQLParseResult
	if err := sonic.Unmarshal(out.Bytes(), &result); err != nil {
		return nil, err
	}
	if result.Error != "" {
		return nil, errors.New(result.Error)
	}
	return &result, nil
}
