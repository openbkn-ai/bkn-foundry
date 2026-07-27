// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSafeQuerySummaryDoesNotLeakSQLOrArguments(t *testing.T) {
	query := "SELECT email FROM customers WHERE email = ?"
	summary := SafeQuerySummary(query, 1)

	for _, forbidden := range []string{"SELECT", "customers", "email", "?"} {
		if strings.Contains(summary, forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, summary)
		}
	}
	for _, required := range []string{"query_hash=sha256:", "query_length=", "argument_count=1"} {
		if !strings.Contains(summary, required) {
			t.Fatalf("summary missing %q: %s", required, summary)
		}
	}
	if summary != SafeQuerySummary(query, 1) {
		t.Fatalf("summary must be stable for the same query")
	}
}

func TestSafeErrorSummaryDoesNotLeakDependencyBody(t *testing.T) {
	err := fmt.Errorf("customer email secret@example.com")
	summary := SafeErrorSummary(err)
	for _, forbidden := range []string{"customer", "email", "secret@example.com"} {
		if strings.Contains(summary, forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, summary)
		}
	}
	for _, required := range []string{"error=true", "error_type=", "error_hash=sha256:", "error_length="} {
		if !strings.Contains(summary, required) {
			t.Fatalf("summary missing %q: %s", required, summary)
		}
	}
}

func TestSensitiveAdaptersUseSafeErrorLogging(t *testing.T) {
	adapterRoot := filepath.Join("..", "drivenadapters")
	rawStructuredError := regexp.MustCompile(`logger\.(Errorf|Warnf)\([^\n]*,\s*err\)`)
	err := filepath.WalkDir(adapterRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(source)
		if strings.Contains(content, "otellog.LogError(") {
			t.Errorf("%s directly logs raw errors; use common.LogSafeError", path)
		}
		for _, unsafeFragment := range []string{
			".Error()",
			"result is [%s]",
			"response code is [%d], result is",
			"small model info: [%s]",
		} {
			if strings.Contains(content, unsafeFragment) {
				t.Errorf("%s logs sensitive dependency data via %q", path, unsafeFragment)
			}
		}
		if rawStructuredError.MatchString(content) {
			t.Errorf("%s directly formats a raw error", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", adapterRoot, err)
	}
}
