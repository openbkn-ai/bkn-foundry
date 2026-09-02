// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import "testing"

func TestActionExecutionPEPRequiresAuthentication(t *testing.T) {
	t.Setenv("ACTION_EXECUTION_PEP_ENABLED", "true")

	t.Setenv("AUTH_ENABLED", "false")
	if GetActionExecutionPEPEnabled() {
		t.Fatal("GetActionExecutionPEPEnabled() = true while authentication is disabled")
	}

	t.Setenv("AUTH_ENABLED", "true")
	if !GetActionExecutionPEPEnabled() {
		t.Fatal("GetActionExecutionPEPEnabled() = false while both switches are enabled")
	}
}
