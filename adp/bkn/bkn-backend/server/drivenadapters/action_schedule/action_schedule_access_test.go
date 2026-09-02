// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_schedule

import (
	"strings"
	"testing"
)

func TestBuildSelectQueryKeepsSchemaCompatibilityWhilePEPIsDisabled(t *testing.T) {
	access := &actionScheduleAccess{}

	t.Setenv("ACTION_EXECUTION_PEP_ENABLED", "false")
	disabledSQL, _, err := access.buildSelectQuery().ToSql()
	if err != nil {
		t.Fatalf("disabled query: %v", err)
	}
	if strings.Contains(disabledSQL, "f_execution_subject") {
		t.Fatalf("disabled query references the migrated column: %s", disabledSQL)
	}

	t.Setenv("ACTION_EXECUTION_PEP_ENABLED", "true")
	enabledSQL, _, err := access.buildSelectQuery().ToSql()
	if err != nil {
		t.Fatalf("enabled query: %v", err)
	}
	if !strings.Contains(enabledSQL, "f_execution_subject") || !strings.Contains(enabledSQL, "f_execution_subject_type") {
		t.Fatalf("enabled query omits execution subject columns: %s", enabledSQL)
	}
}
