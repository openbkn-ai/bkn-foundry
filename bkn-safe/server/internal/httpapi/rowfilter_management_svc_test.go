// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
)

func TestRowFilterManagementServicesUsesDefaultDepartmentLimit(t *testing.T) {
	services := newRowFilterManagementServices(nil, nil, 0, nil).(*rowFilterManagementServices)
	if services.departmentMax != directory.DefaultRowFilterDepartmentScopeLimit {
		t.Fatalf("department limit = %d, want %d", services.departmentMax, directory.DefaultRowFilterDepartmentScopeLimit)
	}
}
