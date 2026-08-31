// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	v013 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v013"
	v014 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v014"
	v015 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v015"
	v016 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v016"
	v017 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v017"
	v018 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v018"
	v019 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v019"
	v020 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v020"
	v021 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v021"
	v022 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v022"
)

// Migration is immutable once released. Every statement in SQL must be safe to
// re-run because MariaDB DDL commits before its ledger row can be recorded.
// Add schema changes as a new version; never edit an existing migration file.
type Migration struct {
	Version  string
	Checksum string
	SQL      string
}

func Migrations() []Migration {
	return newMigrationManifest([]struct {
		version string
		sql     string
	}{
		{version: "013", sql: v013.SchemaSQL()},
		{version: "014", sql: v014.SchemaSQL()},
		{version: "015", sql: v015.SchemaSQL()},
		{version: "016", sql: v016.SchemaSQL()},
		{version: "017", sql: v017.SchemaSQL()},
		{version: "018", sql: v018.SchemaSQL()},
		{version: "019", sql: v019.SchemaSQL()},
		{version: "020", sql: v020.SchemaSQL()},
		{version: "021", sql: v021.SchemaSQL()},
		{version: "022", sql: v022.SchemaSQL()},
	})
}

func newMigrationManifest(entries []struct{ version, sql string }) []Migration {
	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		sum := sha256.Sum256([]byte(entry.sql))
		migrations = append(migrations, Migration{
			Version: entry.version, Checksum: hex.EncodeToString(sum[:]), SQL: entry.sql,
		})
	}
	return migrations
}

func SchemaSQL() string {
	migrations := Migrations()
	schema := make([]string, 0, len(migrations))
	for _, migration := range migrations {
		schema = append(schema, migration.SQL)
	}
	return strings.Join(schema, "\n")
}
