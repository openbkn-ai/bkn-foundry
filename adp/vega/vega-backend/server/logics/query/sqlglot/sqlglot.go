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

	"vega-backend/interfaces"
)

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
	default:
		logger.Errorf("unsupported dataSourceType: %s", dataSourceType)
		return "", fmt.Errorf("unsupported dataSourceType: %s", dataSourceType)
	}
}

// TranspileSQL converts SQL from one dialect to another
func TranspileSQL(ctx context.Context, sql string, fromDialect string, dataSourceType string) (*SQLParseResult, error) {

	// Map the data source type to the sqlglot dialect
	toDialect, err := MapDataSourceTypeToDialect(dataSourceType)
	if err != nil {
		logger.Errorf("MapDataSourceTypeToDialect failed, %s", err.Error())
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "python3", "-c", `
import sys
import json
import sqlglot

try:
    sql = sys.argv[1]
    from_dialect = sys.argv[2]
    to_dialect = sys.argv[3]
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
