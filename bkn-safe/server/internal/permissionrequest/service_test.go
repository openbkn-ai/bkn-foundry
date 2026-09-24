package permissionrequest

import (
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/finegrained"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
	"gorm.io/gorm"
)

func TestApprovalCreatesOneIndependentGrant(t *testing.T) {
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Edition: licverify.EditionProfessional}
	}))
	t.Cleanup(entitlement.ResetForTest)
	db, err := gorm.Open(sqlite.Open("file:permission-request-approval?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	enforcer, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"requester", "reviewer"} {
		if err := db.Create(&model.User{ID: id, Account: id, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.ResourceType{ID: "knowledge_network", Name: "Knowledge Network"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := enforcer.GrantCommunityBundle("reviewer", "knowledge_network", "r-1", authz.AuthoritySourceAdminAuthz); err != nil {
		t.Fatal(err)
	}
	service := New(db, enforcer)
	created, first, err := service.Create(t.Context(), CreateInput{RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: authz.ActFullBusinessAccess, Reason: "missing access"})
	if err != nil || !first {
		t.Fatalf("Create() = %v, %v", first, err)
	}
	replay, second, err := service.Create(t.Context(), CreateInput{RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: authz.ActFullBusinessAccess, Reason: "missing access"})
	if err != nil || second || replay.ID != created.ID {
		t.Fatalf("Create replay = %#v, %v, %v", replay, second, err)
	}
	approved, err := service.Decide(t.Context(), created.ID, DecisionInput{ReviewerID: "reviewer", Decision: "approve"})
	if err != nil || approved.Status != StatusGranted {
		t.Fatalf("Decide() = %#v, %v", approved, err)
	}
	var grant model.AuthorizationGrant
	if err := db.First(&grant, "grant_id = ?", created.GrantID).Error; err != nil {
		t.Fatal(err)
	}
	if grant.AccessorID != "requester" || grant.CreatedBy != "reviewer" {
		t.Fatalf("unexpected grant: %#v", grant)
	}
	if _, err := service.Decide(t.Context(), created.ID, DecisionInput{ReviewerID: "reviewer", Decision: "approve"}); err != ErrClosed {
		t.Fatalf("second approval = %v, want ErrClosed", err)
	}
	if _, _, err := service.Create(t.Context(), CreateInput{RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: authz.ActFullBusinessAccess, Reason: "access needed again"}); err != ErrPermissionAlreadyGranted {
		t.Fatalf("Create after granted = %v, want ErrPermissionAlreadyGranted", err)
	}
}

func TestRequesterCannotApproveOwnRequest(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:permission-request-self-review?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	enforcer, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: "requester", Account: "requester", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	service := New(db, enforcer)
	req, _, err := service.Create(t.Context(), CreateInput{RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: authz.ActFullBusinessAccess, Reason: "missing access"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Decide(t.Context(), req.ID, DecisionInput{ReviewerID: "requester", Decision: "approve"}); err != ErrForbidden {
		t.Fatalf("self approval = %v, want ErrForbidden", err)
	}
}

func TestApprovalInvalidatesRequestWhenPrerequisiteWasRevoked(t *testing.T) {
	entitlement.ResetForTest()
	finegrained.ResetForTest()
	t.Cleanup(func() {
		finegrained.ResetForTest()
		entitlement.ResetForTest()
	})
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Edition: licverify.EditionProfessional}
	}))
	finegrained.Register(licverify.EditionProfessional)
	db, err := gorm.Open(sqlite.Open("file:permission-request-invalidated?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	enforcer, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"requester", "reviewer"} {
		if err := db.Create(&model.User{ID: id, Account: id, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.ResourceType{ID: "knowledge_network", Name: "Knowledge Network"}).Error; err != nil {
		t.Fatal(err)
	}
	grantable := true
	if err := db.Create(&[]model.Operation{
		{ResourceTypeID: "knowledge_network", ID: "view_detail", Name: "View detail", Grantable: &grantable},
		{ResourceTypeID: "knowledge_network", ID: "delete", Name: "Delete", RequiredOperationIDs: "view_detail", Grantable: &grantable},
		{ResourceTypeID: "knowledge_network", ID: "authorize", Name: "Authorize", Grantable: &grantable},
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"view_detail", "delete", "authorize"} {
		if err := enforcer.GrantObjectPermission("reviewer", "knowledge_network", "r-1", operation); err != nil {
			t.Fatal(err)
		}
	}
	service := New(db, enforcer)
	if _, _, err := service.Create(t.Context(), CreateInput{
		RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operations: []string{"delete"}, Reason: "delete access",
	}); !errors.Is(err, ErrPrerequisiteMissing) {
		t.Fatalf("Create without view_detail = %v, want ErrPrerequisiteMissing", err)
	}
	if err := enforcer.GrantObjectPermission("requester", "knowledge_network", "r-1", "view_detail"); err != nil {
		t.Fatal(err)
	}
	request, _, err := service.Create(t.Context(), CreateInput{
		RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operations: []string{"delete"}, Reason: "delete access",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := enforcer.RevokeObjectPermission("requester", "knowledge_network", "r-1", "view_detail"); err != nil {
		t.Fatal(err)
	}

	result, err := service.Decide(t.Context(), request.ID, DecisionInput{ReviewerID: "reviewer", Decision: "approve"})
	if err != nil || result.Status != StatusInvalidated {
		t.Fatalf("Decide() = %#v, %v; want invalidated request", result, err)
	}
	allowed, err := enforcer.CheckContext(t.Context(), "requester", "knowledge_network", "r-1", "delete")
	if err != nil || allowed {
		t.Fatalf("delete grant = %v, %v; want no grant", allowed, err)
	}
	decisions, err := service.ListDecisions(t.Context(), request.ID)
	if err != nil || len(decisions) != 1 || decisions[0].Decision != DecisionInvalidated {
		t.Fatalf("decisions = %#v, %v; want one invalidated decision", decisions, err)
	}
	if _, created, err := service.Create(t.Context(), CreateInput{
		RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operations: []string{"view_detail", "delete"}, Reason: "reapply",
	}); err != nil || !created {
		t.Fatalf("reapply = %v, %v; want new request", created, err)
	}
}

func TestCommunityCreateRequiresBundleRootAndForbidsAuthorize(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:permission-request-community-shape?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	enforcer, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	service := New(db, enforcer)
	for _, input := range []CreateInput{
		{RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: "view_detail"},
		{RequesterID: "requester", ResourceType: "resource", ResourceID: "r-1", Operation: authz.ActFullBusinessAccess},
		{RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: "authorize"},
	} {
		if _, _, err := service.Create(t.Context(), input); err != ErrInvalidRequest {
			t.Fatalf("Create(%+v) = %v, want ErrInvalidRequest", input, err)
		}
	}
}

func TestRejectionRemovesOnlyReviewersTodo(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:permission-request-reject?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	enforcer, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"requester", "reviewer"} {
		if err := db.Create(&model.User{ID: id, Account: id, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := enforcer.GrantCommunityBundle("reviewer", "knowledge_network", "r-1", authz.AuthoritySourceAdminAuthz); err != nil {
		t.Fatal(err)
	}
	service := New(db, enforcer)
	req, _, err := service.Create(t.Context(), CreateInput{RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: authz.ActFullBusinessAccess, Reason: "missing access"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Decide(t.Context(), req.ID, DecisionInput{ReviewerID: "reviewer", Decision: "reject"}); err != nil {
		t.Fatal(err)
	}
	todo, err := service.ListTodo(t.Context(), "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if len(todo) != 0 {
		t.Fatalf("todo after rejection = %#v, want empty", todo)
	}
}

func TestPendingRequestListsOnlyUnreviewedEligibleReviewers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:permission-request-pending-reviewers?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	enforcer, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"requester", "reviewer-one", "reviewer-two"} {
		if err := db.Create(&model.User{ID: id, Account: id, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.ResourceType{ID: "knowledge_network", Name: "Knowledge Network"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, reviewer := range []string{"reviewer-one", "reviewer-two"} {
		if err := enforcer.GrantCommunityBundle(reviewer, "knowledge_network", "r-1", authz.AuthoritySourceAdminAuthz); err != nil {
			t.Fatal(err)
		}
	}
	service := New(db, enforcer)
	req, _, err := service.Create(t.Context(), CreateInput{RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: authz.ActFullBusinessAccess, Reason: "missing access"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Decide(t.Context(), req.ID, DecisionInput{ReviewerID: "reviewer-one", Decision: "reject"}); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(t.Context(), req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending || got.ReviewerID != "reviewer-two" || got.ReviewerName != "reviewer-two" {
		t.Fatalf("pending reviewers = %#v, want reviewer-two only", got)
	}
}

func TestRequestedPageFiltersByExactResourceID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:permission-request-resource-id-filter?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: "requester", Account: "requester", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, request := range []model.PermissionRequest{
		{
			ID:           "request-one",
			RequestKey:   "requester|knowledge_network|network-one|view_detail",
			GrantID:      "grant-request-one",
			RequesterID:  "requester",
			ResourceType: "knowledge_network",
			ResourceID:   "network-one",
			ResourceName: "Network one",
			Operation:    "view_detail",
			Status:       StatusPending,
		},
		{
			ID:           "request-two",
			RequestKey:   "requester|knowledge_network|network-two|view_detail",
			GrantID:      "grant-request-two",
			RequesterID:  "requester",
			ResourceType: "knowledge_network",
			ResourceID:   "network-two",
			ResourceName: "Network two",
			Operation:    "view_detail",
			Status:       StatusPending,
		},
	} {
		if err := db.Create(&request).Error; err != nil {
			t.Fatal(err)
		}
	}

	page, err := New(db, nil).ListRequestedPage(t.Context(), "requester", PageOptions{
		Limit:        20,
		ResourceType: "knowledge_network",
		ResourceID:   "network-two",
		Status:       StatusPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 1 || len(page.Entries) != 1 || page.Entries[0].ID != "request-two" {
		t.Fatalf("resource ID filtered page = %#v, want request-two only", page)
	}
}

func TestTodoSummaryCountsOnlyActiveUnreviewedPendingRequests(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:permission-request-todo-summary?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, request := range []model.PermissionRequest{
		{ID: "pending", RequestKey: "pending", GrantID: "grant-pending", RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: "view_detail", Status: StatusPending},
		{ID: "reviewed", RequestKey: "reviewed", GrantID: "grant-reviewed", RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-2", Operation: "view_detail", Status: StatusPending},
		{ID: "revoked", RequestKey: "revoked", GrantID: "grant-revoked", RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-3", Operation: "view_detail", Status: StatusPending},
		{ID: "closed", RequestKey: "closed", GrantID: "grant-closed", RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-4", Operation: "view_detail", Status: StatusGranted},
	} {
		if err := db.Create(&request).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, reviewer := range []model.PermissionRequestReviewer{
		{ID: "candidate-pending", RequestID: "pending", ReviewerID: "reviewer", EligibilityStatus: ReviewerActive},
		{ID: "candidate-reviewed", RequestID: "reviewed", ReviewerID: "reviewer", EligibilityStatus: ReviewerActive},
		{ID: "candidate-revoked", RequestID: "revoked", ReviewerID: "reviewer", EligibilityStatus: ReviewerRevoked},
		{ID: "candidate-closed", RequestID: "closed", ReviewerID: "reviewer", EligibilityStatus: ReviewerActive},
	} {
		if err := db.Create(&reviewer).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.PermissionRequestDecision{ID: "decision", RequestID: "reviewed", ReviewerID: "reviewer", Decision: "reject"}).Error; err != nil {
		t.Fatal(err)
	}

	summary, err := New(db, nil).GetTodoSummary(t.Context(), "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if summary.PendingCount != 1 {
		t.Fatalf("todo summary = %#v, want one active unreviewed pending request", summary)
	}
}
