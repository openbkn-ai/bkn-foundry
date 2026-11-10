// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_version

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Every table with a branch column must be either copied with a version or listed as not
// copied, so a table added later cannot be left out of versions by accident.
func TestEveryBranchTableIsClassified(t *testing.T) {
	initSQL := latestInitSQL(t)
	known := map[string]bool{knTable: true}
	for _, table := range versionedTables {
		known[table.name] = true
	}
	for _, table := range unversionedTables {
		known[table] = true
	}

	tables := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS (\w+) \((.*?)\n\)`).FindAllStringSubmatch(initSQL, -1)
	branchColumn := regexp.MustCompile(`(?m)^\s+f_branch\s`)
	found := 0
	for _, table := range tables {
		if !branchColumn.MatchString(table[2]) {
			continue
		}
		found++
		if !known[table[1]] {
			t.Errorf("table %s has f_branch but is neither in versionedTables nor in unversionedTables", table[1])
		}
	}
	if found < len(versionedTables) {
		t.Fatalf("found %d branch tables in init.sql, want at least %d", found, len(versionedTables))
	}
}

func latestInitSQL(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..", "..", "..", "migrations", "bkn-backend", "mariadb")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	// Version directories are named like 0.2.2; compare them as numbers.
	var latest []int
	latestName := ""
	for _, entry := range entries {
		parts := strings.Split(entry.Name(), ".")
		numbers := make([]int, 0, len(parts))
		for _, part := range parts {
			n, err := strconv.Atoi(part)
			if err != nil {
				numbers = nil
				break
			}
			numbers = append(numbers, n)
		}
		if entry.IsDir() && numbers != nil && slices.Compare(numbers, latest) > 0 {
			latest, latestName = numbers, entry.Name()
		}
	}
	if latestName == "" {
		t.Fatal("no migration versions")
	}
	content, err := os.ReadFile(filepath.Join(root, latestName, "init.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
