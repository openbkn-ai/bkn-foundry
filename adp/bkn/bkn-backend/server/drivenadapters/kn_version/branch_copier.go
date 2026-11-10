// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kn_version stores knowledge network versions and copies their rows between branches.
package kn_version

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const knTable = "t_knowledge_network"

// versionedTable is a definition table copied with a version. Copies keep the row id, except:
// newID tables have a surrogate VARCHAR key that gets a new value, and autoID tables have an
// AUTO_INCREMENT key the database assigns.
type versionedTable struct {
	name   string
	newID  bool
	autoID bool
}

// versionedTables are copied in this order and deleted in the reverse order, so that a row is
// inserted after the rows it references and deleted before them.
var versionedTables = []versionedTable{
	{name: "t_object_type"},
	// Object types are read with an inner join on their status row.
	{name: "t_object_type_status"},
	{name: "t_relation_type"},
	{name: "t_action_type"},
	{name: "t_concept_group"},
	{name: "t_concept_group_relation", newID: true},
	{name: "t_value_type"},
	{name: "t_risk_type"},
	// Proxy grants identify a capability by its binding id, so the id is the same on every branch.
	{name: "t_kn_capability_binding"},
	{name: "t_object_metric_definition"},
	// Has a foreign key on t_object_metric_definition.
	{name: "t_object_metric_reference", autoID: true},
}

// Tables with a branch column that are not copied: schedules belong to the network and stay on
// main, and t_metric_definition is a legacy table no code reads or writes.
var unversionedTables = []string{"t_action_schedule", "t_metric_definition"}

var (
	branchCopierOnce sync.Once
	branchCopierInst interfaces.BranchCopier
)

type branchCopier struct {
	db *sql.DB

	mu      sync.Mutex
	columns map[string][]string
}

func NewBranchCopier(appSetting *common.AppSetting) interfaces.BranchCopier {
	branchCopierOnce.Do(func() {
		branchCopierInst = &branchCopier{db: libdb.NewDB(&appSetting.DBSetting), columns: map[string][]string{}}
	})
	return branchCopierInst
}

func (bc *branchCopier) ReplaceBranch(ctx context.Context, tx *sql.Tx, knID, src, dst string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Replace knowledge network branch")
	defer span.End()
	span.SetAttributes(attr.Key("kn_id").String(knID), attr.Key("src").String(src), attr.Key("dst").String(dst))

	if src == dst {
		return fmt.Errorf("replace branch %q with itself", src)
	}
	if err := bc.deleteDefinitions(ctx, tx, knID, dst); err != nil {
		return err
	}
	for _, table := range versionedTables {
		if err := bc.copyRows(ctx, tx, table, knID, src, dst); err != nil {
			return err
		}
	}
	return bc.copyNetworkRow(ctx, tx, knID, src, dst)
}

func (bc *branchCopier) DeleteBranch(ctx context.Context, tx *sql.Tx, knID, branch string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Delete knowledge network branch")
	defer span.End()
	span.SetAttributes(attr.Key("kn_id").String(knID), attr.Key("branch").String(branch))

	if err := bc.deleteDefinitions(ctx, tx, knID, branch); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM "+knTable+" WHERE f_id = ? AND f_branch = ?", knID, branch)
	return err
}

func (bc *branchCopier) BranchesWithData(ctx context.Context, tx *sql.Tx, knID string) ([]string, error) {
	parts := []string{"SELECT f_branch FROM " + knTable + " WHERE f_id = ?"}
	args := []any{knID}
	for _, table := range versionedTables {
		parts = append(parts, "SELECT f_branch FROM "+table.name+" WHERE f_kn_id = ?")
		args = append(args, knID)
	}
	rows, err := tx.QueryContext(ctx, strings.Join(parts, " UNION ")+" ORDER BY f_branch", args...)
	if err != nil {
		return nil, fmt.Errorf("list the branches of network %s: %w", knID, err)
	}
	defer func() { _ = rows.Close() }()
	branches := []string{}
	for rows.Next() {
		var branch string
		if err := rows.Scan(&branch); err != nil {
			return nil, err
		}
		branches = append(branches, branch)
	}
	return branches, rows.Err()
}

func (bc *branchCopier) deleteDefinitions(ctx context.Context, tx *sql.Tx, knID, branch string) error {
	for i := len(versionedTables) - 1; i >= 0; i-- {
		query := "DELETE FROM " + versionedTables[i].name + " WHERE f_kn_id = ? AND f_branch = ?"
		if _, err := tx.ExecContext(ctx, query, knID, branch); err != nil {
			return fmt.Errorf("delete %s on branch %s: %w", versionedTables[i].name, branch, err)
		}
	}
	return nil
}

// copyRows copies one table with INSERT ... SELECT over every column of the table, so a column
// added later is copied without a code change.
func (bc *branchCopier) copyRows(ctx context.Context, tx *sql.Tx, table versionedTable, knID, src, dst string) error {
	columns, err := bc.tableColumns(ctx, tx, table.name)
	if err != nil {
		return err
	}
	insert := make([]string, 0, len(columns))
	selects := make([]string, 0, len(columns))
	args := []any{}
	for _, column := range columns {
		switch {
		case column == "f_id" && table.autoID:
			continue
		case column == "f_id" && table.newID:
			selects = append(selects, "REPLACE(UUID(), '-', '')")
		case column == "f_branch":
			selects = append(selects, "?")
			args = append(args, dst)
		default:
			selects = append(selects, column)
		}
		insert = append(insert, column)
	}
	query := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s WHERE f_kn_id = ? AND f_branch = ?",
		table.name, strings.Join(insert, ", "), strings.Join(selects, ", "), table.name)
	if _, err := tx.ExecContext(ctx, query, append(args, knID, src)...); err != nil {
		return fmt.Errorf("copy %s from %s to %s: %w", table.name, src, dst, err)
	}
	return nil
}

// copyNetworkRow creates the network row of dst from src, or, when dst already has one, carries
// over only the content that belongs to a version. Basic info is the same on every branch.
func (bc *branchCopier) copyNetworkRow(ctx context.Context, tx *sql.Tx, knID, src, dst string) error {
	columns, err := bc.tableColumns(ctx, tx, knTable)
	if err != nil {
		return err
	}
	selects := make([]string, len(columns))
	for i, column := range columns {
		selects[i] = column
		if column == "f_branch" {
			selects[i] = "?"
		}
	}
	query := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s WHERE f_id = ? AND f_branch = ? "+
		"ON DUPLICATE KEY UPDATE f_bkn_raw_content = VALUES(f_bkn_raw_content), f_skill_content = VALUES(f_skill_content)",
		knTable, strings.Join(columns, ", "), strings.Join(selects, ", "), knTable)
	result, err := tx.ExecContext(ctx, query, dst, knID, src)
	if err != nil {
		return fmt.Errorf("copy the network row from %s to %s: %w", src, dst, err)
	}
	// 0 rows: no source row, or the target already holds the same content.
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		var one int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM "+knTable+" WHERE f_id = ? AND f_branch = ?", knID, src).Scan(&one)
		if err == sql.ErrNoRows {
			return fmt.Errorf("network %s has no row on branch %s: %w", knID, src, common.ErrRowNotFound)
		}
		return err
	}
	return nil
}

func (bc *branchCopier) tableColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	bc.mu.Lock()
	cached, ok := bc.columns[table]
	bc.mu.Unlock()
	if ok {
		return cached, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT column_name FROM information_schema.columns "+
		"WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ordinal_position", table)
	if err != nil {
		return nil, fmt.Errorf("read the columns of %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	columns := []string{}
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("table %s has no columns", table)
	}
	bc.mu.Lock()
	bc.columns[table] = columns
	bc.mu.Unlock()
	return columns, nil
}
