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
	"errors"
	"os/exec"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-comm-go/logger"
)

const rejectedPrefix = "READ_ONLY_SQL_REJECTED:"

// Adapter validates a query against the Raw Query policy.
type Adapter interface {
	ValidateSQL(sql string, inputDialect string) error
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

func (a *SQLGlotAdapter) ValidateSQL(sql string, inputDialect string) error {
	cmd := exec.Command("python3", "-c", validationScript, sql, inputDialect)

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		logger.Errorf("ValidateSQL policy failed, %s", err.Error())
		return err
	}

	var result validationResult
	if err := sonic.Unmarshal(out.Bytes(), &result); err != nil {
		logger.Errorf("ValidateSQL policy failed, %s", err.Error())
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

const validationScript = `
import json
import sys

import sqlglot
from sqlglot import exp

REJECTED_PREFIX = "READ_ONLY_SQL_REJECTED:"
FORBIDDEN_NODE_NAMES = {
    "Into", "Lock", "SessionParameter", "Set", "Command", "Transaction",
}
FORBIDDEN_FUNCTIONS = {
    "benchmark", "load_file", "pg_sleep", "sleep",
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
            if name in FORBIDDEN_FUNCTIONS:
                reject("unsupported function: " + name)

    print(json.dumps({"error": None}))
except Exception as e:
    print(json.dumps({"error": REJECTED_PREFIX + "invalid SQL: " + str(e)}))
`
