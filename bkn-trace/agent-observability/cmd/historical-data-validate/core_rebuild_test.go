package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCoreRebuildRequiresDeployedConfiguration(t *testing.T) {
	t.Setenv("BKN_TRACE_CORE_MARIADB_DSN", "")
	t.Setenv("OPENSEARCH_ENDPOINT", "")
	t.Setenv("BKN_TRACE_PROJECTION_INDEX", "")
	if err := runCommand([]string{"--rebuild-core-projection"}, bytes.NewReader(nil), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "rebuild configuration missing") {
		t.Fatalf("expected explicit rebuild configuration check, got %v", err)
	}
}

func TestCoreRebuildCannotBeCombinedWithPublication(t *testing.T) {
	if err := runCommand([]string{"--rebuild-core-projection", "--publish-audit", "--in-place-upgrade"}, bytes.NewReader(nil), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("expected conflicting mode rejection, got %v", err)
	}
}
