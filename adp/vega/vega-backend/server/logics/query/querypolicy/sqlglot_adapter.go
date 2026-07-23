// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package querypolicy defines the Raw Query allowlist independently from SQL
// dialect compilation and database execution.
package querypolicy

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-comm-go/logger"
)

const rejectedPrefix = "READ_ONLY_SQL_REJECTED:"

// Adapter validates a query against the Raw Query policy.
type Adapter interface {
	ExtractTableResourceIDs(ctx context.Context, sql string, inputDialect string) ([]string, error)
	ValidateSQL(ctx context.Context, sql string, inputDialect string) error
	ValidateTableReferences(ctx context.Context, sql string, inputDialect string, allowedReferences []string) error
}

// ReadOnlySQLValidationError indicates that SQL is outside the intentionally
// narrow Raw Query read-only subset.
type ReadOnlySQLValidationError struct {
	Reason string
}

func (e *ReadOnlySQLValidationError) Error() string {
	return e.Reason
}

// SQLGlotAdapter implements the policy by inspecting SQLGlot's AST.
type SQLGlotAdapter struct{}

func NewSQLGlotAdapter() *SQLGlotAdapter {
	return &SQLGlotAdapter{}
}

func (a *SQLGlotAdapter) ExtractTableResourceIDs(ctx context.Context, sql string, inputDialect string) ([]string, error) {
	return ExtractTableResourceIDs(ctx, sql, inputDialect)
}

func (a *SQLGlotAdapter) ValidateSQL(ctx context.Context, sql string, inputDialect string) error {
	cmd := exec.CommandContext(ctx, "python3", "-c", validationScript, sql, inputDialect)

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		logger.Errorf("ValidateSQL policy command failed")
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}

	var result validationResult
	if err := sonic.Unmarshal(out.Bytes(), &result); err != nil {
		logger.Errorf("ValidateSQL policy response decode failed")
		return err
	}
	if result.Error == "" {
		return nil
	}
	if strings.HasPrefix(result.Error, rejectedPrefix) {
		return &ReadOnlySQLValidationError{
			Reason: strings.TrimSpace(strings.TrimPrefix(result.Error, rejectedPrefix)),
		}
	}
	return errors.New(result.Error)
}

// ValidateTableReferences verifies that every physical table parsed from sql is
// one of the server-resolved Resource source identifiers. It deliberately
// rejects a query when a source identifier cannot be parsed, rather than
// weakening the resource permission boundary.
func (a *SQLGlotAdapter) ValidateTableReferences(ctx context.Context, sql string, inputDialect string, allowedReferences []string) error {
	allowedJSON, err := sonic.Marshal(allowedReferences)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "python3", "-c", tableReferenceValidationScript, sql, inputDialect, string(allowedJSON))

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		logger.Errorf("ValidateTableReferences policy command failed")
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}

	var result validationResult
	if err := sonic.Unmarshal(out.Bytes(), &result); err != nil {
		logger.Errorf("ValidateTableReferences policy response decode failed")
		return err
	}
	if result.Error == "" {
		return nil
	}
	if strings.HasPrefix(result.Error, rejectedPrefix) {
		return &ReadOnlySQLValidationError{
			Reason: strings.TrimSpace(strings.TrimPrefix(result.Error, rejectedPrefix)),
		}
	}
	return errors.New(result.Error)
}

type validationResult struct {
	Error string `json:"error"`
}

type resourceIDExtractionResult struct {
	ResourceIDs []string `json:"resource_ids"`
	Error       string   `json:"error"`
}

// ExtractTableResourceIDs returns Resource IDs only when their placeholders
// occur in SQL Table nodes. Comments and string literals are intentionally
// left untouched before parsing, so they can never establish a binding.
func ExtractTableResourceIDs(ctx context.Context, sql string, inputDialect string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "python3", "-c", resourceIDExtractionScript, sql, inputDialect)

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		logger.Errorf("ExtractTableResourceIDs policy command failed")
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	var result resourceIDExtractionResult
	if err := sonic.Unmarshal(out.Bytes(), &result); err != nil {
		logger.Errorf("ExtractTableResourceIDs policy response decode failed")
		return nil, err
	}
	if result.Error != "" {
		return nil, errors.New("invalid SQL resource placeholder")
	}
	return result.ResourceIDs, nil
}

const resourceIDExtractionScript = `
import json
import re
import secrets
import sys

import sqlglot
from sqlglot import exp

PLACEHOLDER = re.compile(r"\{\{\.?([a-z0-9][a-z0-9_-]{0,39})\}\}")

def replace_code_placeholders(sql):
    marker_prefix = "__bkn_resource_" + secrets.token_hex(16) + "_"
    resource_by_marker = {}
    output = []
    index = 0
    marker_index = 0
    while index < len(sql):
        if sql.startswith("--", index):
            end = sql.find("\n", index)
            if end == -1:
                output.append(sql[index:])
                break
            output.append(sql[index:end + 1])
            index = end + 1
            continue
        if sql.startswith("/*", index):
            end = sql.find("*/", index + 2)
            if end == -1:
                output.append(sql[index:])
                break
            output.append(sql[index:end + 2])
            index = end + 2
            continue
        if sql[index] in "'\"" or sql[index] == chr(96):
            quote = sql[index]
            end = index + 1
            while end < len(sql):
                if sql[end] == quote:
                    if end + 1 < len(sql) and sql[end + 1] == quote:
                        end += 2
                        continue
                    end += 1
                    break
                if dialect == "mysql" and sql[end] == "\\" and quote == "'" and end + 1 < len(sql):
                    end += 2
                    continue
                end += 1
            output.append(sql[index:end])
            index = end
            continue
        match = PLACEHOLDER.match(sql, index)
        if match:
            marker = marker_prefix + str(marker_index)
            marker_index += 1
            resource_by_marker[marker] = match.group(1)
            output.append(marker)
            index = match.end()
            continue
        output.append(sql[index])
        index += 1
    return "".join(output), resource_by_marker

try:
    sql, dialect = sys.argv[1], sys.argv[2]
    transformed, resource_by_marker = replace_code_placeholders(sql)
    statement = sqlglot.parse_one(transformed, read=dialect)
    resource_ids = []
    seen = set()
    for table in statement.find_all(exp.Table):
        resource_id = resource_by_marker.get(table.name)
        if resource_id is not None and resource_id not in seen:
            seen.add(resource_id)
            resource_ids.append(resource_id)
    print(json.dumps({"resource_ids": resource_ids, "error": None}))
except Exception as e:
    print(json.dumps({"resource_ids": [], "error": "invalid SQL resource placeholder: " + str(e)}))
`

const validationScript = `
import json
import sys

import sqlglot
from sqlglot import exp

REJECTED_PREFIX = "READ_ONLY_SQL_REJECTED:"
FORBIDDEN_NODE_NAMES = {
    "Into", "Lock", "SessionParameter", "Set", "Command", "Transaction",
    "With", "Union", "Intersect", "Except", "Insert", "Update", "Delete",
    "Merge", "Copy", "Create", "Alter", "Drop", "Truncate", "Grant", "Revoke",
}
FORBIDDEN_FUNCTIONS = {
    "benchmark", "dblink", "http_get", "load_file", "lo_import",
    "pg_read_file", "pg_sleep", "read_csv", "read_csv_auto", "read_json",
    "read_json_auto", "read_parquet", "sleep", "system", "sys_exec", "xp_cmdshell",
}

def reject(reason):
    print(json.dumps({"error": REJECTED_PREFIX + reason}))
    sys.exit(0)

try:
    sql = sys.argv[1]
    dialect = sys.argv[2]
    statements = sqlglot.parse(sql, read=dialect)
    if len(statements) != 1:
        reject("exactly one SELECT statement is required")

    statement = statements[0]
    if type(statement) is not exp.Select:
        reject("only a top-level SELECT statement is supported")
    if statement.args.get("with") is not None or statement.args.get("with_") is not None:
        reject("WITH queries are not supported")
    if statement.args.get("into") is not None or statement.args.get("locks"):
        reject("SELECT INTO and locking reads are not supported")

    for node in statement.walk():
        if type(node).__name__ in FORBIDDEN_NODE_NAMES:
            reject("unsupported SQL construct: " + type(node).__name__)
        if isinstance(node, exp.Func):
            name = str(getattr(node, "name", "")).lower()
            # Unknown functions are UDFs in SQLGlot. They cannot be proven
            # read-only, so the Raw Query policy rejects them by default.
            if isinstance(node, exp.Anonymous):
                reject("unsupported function")
            if name in FORBIDDEN_FUNCTIONS:
                reject("unsupported function: " + name)

    print(json.dumps({"error": None}))
except Exception as e:
    print(json.dumps({"error": REJECTED_PREFIX + "invalid SQL: " + str(e)}))
`

const tableReferenceValidationScript = `
import json
import sys

import sqlglot
from sqlglot import exp

REJECTED_PREFIX = "READ_ONLY_SQL_REJECTED:"

def reject(reason):
    print(json.dumps({"error": REJECTED_PREFIX + reason}))
    sys.exit(0)

def canonical_identifier(identifier):
    if identifier is None:
        return None
    name = identifier.name
    if identifier.args.get("quoted"):
        return ("quoted", name)
    return ("unquoted", name.lower())

def canonical_table(table):
    return (
        canonical_identifier(table.args.get("catalog")),
        canonical_identifier(table.args.get("db")),
        canonical_identifier(table.this),
    )

try:
    sql = sys.argv[1]
    dialect = sys.argv[2]
    allowed_references = json.loads(sys.argv[3])

    allowed = set()
    for reference in allowed_references:
        source = sqlglot.parse_one("SELECT 1 FROM " + reference, read=dialect)
        tables = list(source.find_all(exp.Table))
        if len(tables) != 1:
            reject("invalid resource source identifier")
        allowed.add(canonical_table(tables[0]))

    statement = sqlglot.parse_one(sql, read=dialect)
    for table in statement.find_all(exp.Table):
        if canonical_table(table) not in allowed:
            reject("SQL references an unbound physical table")
    print(json.dumps({"error": None}))
except Exception as e:
    print(json.dumps({"error": REJECTED_PREFIX + "invalid SQL table reference: " + str(e)}))
`
