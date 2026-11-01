// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManualBuildMigrationTargetsConfiguredDefaultDatabase(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "migrations", "20261101-build-grants.sql"))
	if err != nil {
		t.Fatal(err)
	}
	target := "USE " + defaultConfig().DB.Name + ";"
	if !strings.Contains(string(data), "\n"+target+"\n") {
		t.Fatal("manual build migration does not target the service's default database")
	}
}
