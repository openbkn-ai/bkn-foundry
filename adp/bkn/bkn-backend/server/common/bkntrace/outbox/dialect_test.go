// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package outbox

import (
	"strings"
	"testing"
)

func TestParseDatabaseDialect(t *testing.T) {
	tests := []struct {
		value string
		want  databaseDialect
		fail  bool
	}{
		{value: "", want: dialectMariaDB},
		{value: "mariadb", want: dialectMariaDB},
		{value: "DM8", want: dialectDM8},
		{value: "unsupported", fail: true},
	}
	for _, test := range tests {
		got, err := parseDatabaseDialect(test.value)
		if test.fail {
			if err == nil {
				t.Fatalf("parseDatabaseDialect(%q) should fail", test.value)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Fatalf("parseDatabaseDialect(%q) = %q, %v; want %q, nil", test.value, got, err, test.want)
		}
	}
}

func TestDM8HeadOfLineAndEpochSQL(t *testing.T) {
	repository := &Repository{dialect: dialectDM8}
	head := repository.claimHeadOfLineSQL()
	if !strings.Contains(head, "ROWNUM = 1") || strings.Contains(head, "LIMIT") {
		t.Fatalf("DM8 head-of-line SQL must use ROWNUM, got %s", head)
	}
	ensure := repository.ensureStreamStateSQL()
	if !strings.Contains(ensure, "MERGE INTO") || strings.Contains(ensure, "WHERE NOT EXISTS") {
		t.Fatalf("DM8 stream initialization must use MERGE, got %s", ensure)
	}
}

func TestDM8CleanupSQL(t *testing.T) {
	repository := &Repository{dialect: dialectDM8}
	if query := repository.deleteOutboxSQL("delivered_at"); !strings.Contains(query, "ROWNUM <= ?") || strings.Contains(query, "LIMIT") {
		t.Fatalf("DM8 cleanup SQL must use ROWNUM, got %s", query)
	}
	if query := repository.deleteExpiredAuditsSQL(); !strings.Contains(query, "ROWNUM <= ?") || strings.Contains(query, "LIMIT") {
		t.Fatalf("DM8 audit cleanup SQL must use ROWNUM, got %s", query)
	}
}
