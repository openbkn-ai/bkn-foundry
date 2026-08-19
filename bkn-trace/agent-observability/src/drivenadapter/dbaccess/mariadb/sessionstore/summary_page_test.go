package sessionstore

import (
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestSummaryOwnerWherePreservesLegacySubjectBoundary(t *testing.T) {
	where, args := summaryOwnerWhere("r", isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{
		TenantID: "tenant-1", BusinessDomain: "domain-1", AccountID: "subject-1", AccountType: "service",
	}, BusinessDomain: "other-domain"})
	wantWhere := []string{
		"r.tenant_id=?", "r.business_domain_id=?", "r.effective_subject_type=?", "r.effective_subject_id=?",
	}
	wantArgs := []any{"tenant-1", "domain-1", "service", "subject-1"}
	if !reflect.DeepEqual(where, wantWhere) || !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("where=%v args=%v", where, args)
	}
}
