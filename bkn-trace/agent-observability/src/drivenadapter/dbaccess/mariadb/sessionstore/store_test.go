// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"strings"
	"testing"
	"time"
)

func TestTransactionRetryDelayUsesBoundedExponentialJitter(t *testing.T) {
	t.Parallel()

	for attempt := 0; attempt < transactionRetries; attempt++ {
		maximum := 5 * time.Millisecond * time.Duration(1<<attempt)
		for sample := 0; sample < 100; sample++ {
			delay := transactionRetryDelay(attempt)
			if delay < 0 || delay > maximum {
				t.Fatalf("attempt %d delay %s exceeds [0,%s]", attempt, delay, maximum)
			}
		}
	}
}

func TestMigrationPlanRejectsDatabaseAheadOfBinary(t *testing.T) {
	_, err := migrationPlan(Migrations(), map[string]string{"999": "unknown"})
	if err == nil || !strings.Contains(err.Error(), "newer than this BKN Trace image") {
		t.Fatalf("expected database-ahead error, got %v", err)
	}
}

func TestMigrationPlanRejectsChecksumDrift(t *testing.T) {
	migration := Migrations()[0]
	_, err := migrationPlan(Migrations(), map[string]string{migration.Version: "changed"})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
}

func TestMigrationPlanReturnsOnlyUnappliedVersions(t *testing.T) {
	migrations := Migrations()
	applied := map[string]string{migrations[0].Version: migrations[0].Checksum}
	plan, err := migrationPlan(migrations, applied)
	if err != nil {
		t.Fatalf("plan migrations: %v", err)
	}
	if len(plan) != len(migrations)-1 || plan[0].Version != migrations[1].Version {
		t.Fatalf("unexpected migration plan: %#v", plan)
	}
}

func TestMigrationPlanUpgradesExistingCoreSchemaWithProvenanceTable(t *testing.T) {
	migrations := Migrations()
	applied := make(map[string]string, len(migrations)-1)
	for _, migration := range migrations[:len(migrations)-1] {
		applied[migration.Version] = migration.Checksum
	}
	plan, err := migrationPlan(migrations, applied)
	if err != nil {
		t.Fatalf("plan provenance schema migration: %v", err)
	}
	if len(plan) != 1 || plan[0].Version != "017" || !strings.Contains(plan[0].SQL, "bkn_trace_ee_provenance_analyses") {
		t.Fatalf("unexpected provenance schema plan: %#v", plan)
	}
}

func TestMigrationPlanRejectsAppliedVersionGap(t *testing.T) {
	migrations := Migrations()
	applied := map[string]string{
		migrations[0].Version: migrations[0].Checksum,
		migrations[2].Version: migrations[2].Checksum,
	}
	if _, err := migrationPlan(migrations, applied); err == nil || !strings.Contains(err.Error(), "not a contiguous prefix") {
		t.Fatalf("expected version-gap error, got %v", err)
	}
}

func TestSplitSQLStatementsKeepsSemicolonsInQuotedValues(t *testing.T) {
	statements, err := splitSQLStatements("INSERT INTO example VALUES ('a;b'); CREATE TABLE test (id INT);")
	if err != nil {
		t.Fatalf("split statements: %v", err)
	}
	if len(statements) != 2 || !strings.Contains(statements[0], "'a;b'") {
		t.Fatalf("unexpected statements: %#v", statements)
	}
}

func TestSplitSQLStatementsIgnoresSemicolonsInComments(t *testing.T) {
	statements, err := splitSQLStatements("-- ignored;\nCREATE TABLE first_table (id INT); # ignored;\n/* ignored; */ CREATE TABLE second_table (id INT);")
	if err != nil {
		t.Fatalf("split statements: %v", err)
	}
	if len(statements) != 2 {
		t.Fatalf("expected two statements, got %#v", statements)
	}
}

func TestSchemaMigrationLockNameIsScopedAndBounded(t *testing.T) {
	first := schemaMigrationLockName("first_database")
	second := schemaMigrationLockName("second_database")
	if first == second || len(first) > 64 {
		t.Fatalf("unexpected scoped lock names: %q and %q", first, second)
	}
}
