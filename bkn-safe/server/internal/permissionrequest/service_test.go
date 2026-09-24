package permissionrequest

import (
	"testing"

	"github.com/glebarez/sqlite"
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
	for _, op := range []string{"authorize", "query_data"} {
		if err := db.Create(&model.Operation{ResourceTypeID: "knowledge_network", ID: op, Name: op}).Error; err != nil {
			t.Fatal(err)
		}
		if err := enforcer.GrantObjectPermission("reviewer", "knowledge_network", "r-1", op); err != nil {
			t.Fatal(err)
		}
	}
	service := New(db, enforcer)
	created, first, err := service.Create(t.Context(), CreateInput{RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: "query_data", Reason: "missing access"})
	if err != nil || !first {
		t.Fatalf("Create() = %v, %v", first, err)
	}
	replay, second, err := service.Create(t.Context(), CreateInput{RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: "query_data", Reason: "missing access"})
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
	reapplied, createdAgain, err := service.Create(t.Context(), CreateInput{RequesterID: "requester", ResourceType: "knowledge_network", ResourceID: "r-1", Operation: "query_data", Reason: "access needed again"})
	if err != nil || !createdAgain {
		t.Fatalf("Create after granted = %v, %v", createdAgain, err)
	}
	if reapplied.ID == created.ID || reapplied.Status != StatusPending {
		t.Fatalf("reapplication = %#v, want a new pending request", reapplied)
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
	if err := db.Create(&model.ResourceType{ID: "resource", Name: "Resource"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Operation{ResourceTypeID: "resource", ID: "query_data", Name: "query_data"}).Error; err != nil {
		t.Fatal(err)
	}
	service := New(db, enforcer)
	req, _, err := service.Create(t.Context(), CreateInput{RequesterID: "requester", ResourceType: "resource", ResourceID: "r-1", Operation: "query_data", Reason: "missing access"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Decide(t.Context(), req.ID, DecisionInput{ReviewerID: "requester", Decision: "approve"}); err != ErrForbidden {
		t.Fatalf("self approval = %v, want ErrForbidden", err)
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
	if err := db.Create(&model.ResourceType{ID: "resource", Name: "Resource"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"authorize", "query_data"} {
		if err := db.Create(&model.Operation{ResourceTypeID: "resource", ID: op, Name: op}).Error; err != nil {
			t.Fatal(err)
		}
		if err := enforcer.GrantObjectPermission("reviewer", "resource", "r-1", op); err != nil {
			t.Fatal(err)
		}
	}
	service := New(db, enforcer)
	req, _, err := service.Create(t.Context(), CreateInput{RequesterID: "requester", ResourceType: "resource", ResourceID: "r-1", Operation: "query_data", Reason: "missing access"})
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
