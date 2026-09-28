package operationaudit

import (
	"context"
	"testing"
)

func TestManagementAuditOwnershipIsRequestLocal(t *testing.T) {
	base := context.Background()
	managed := WithManagementAuditOwner(base)
	if ManagementAuditOwned(base) || !ManagementAuditOwned(managed) || ManagementAuditOwned(nil) {
		t.Fatal("only the registered management request may suppress business Audit")
	}
}
